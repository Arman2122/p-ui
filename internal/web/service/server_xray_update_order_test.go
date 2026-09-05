package service

import (
	"os"
	"strings"
	"testing"
)

/*
The ordering: the download must happen while xray is still running, because
it is xray that carries the panel's egress proxy on a filtered network. The
download itself lives in the xray core now (its Install); what this guards is
that the service calls it, and never a stop, before the restart.

Asserted structurally — UpdateXray must not stop the process itself. Its only
lifecycle call is RestartXray at the end, which runs the -test pre-flight before
it stops anything, so a binary that verifies but cannot load this config is
refused with the old core still serving.
*/
func TestUpdateXrayNeverStopsTheCoreItself(t *testing.T) {
	body, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	start := strings.Index(src, "func (s *ServerService) UpdateXray(version string) error {")
	if start < 0 {
		t.Fatal("UpdateXray not found; this guard is now vacuous")
	}
	end := strings.Index(src[start:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not find the end of UpdateXray")
	}
	fn := src[start : start+end]

	if strings.Contains(fn, "StopXrayService()") {
		t.Fatal("UpdateXray stops the core itself; every failure after that point leaves the box down, and it removes the egress bridge the download needs")
	}
	// The call form, not the bare name: the doc comment above it says the same
	// words, and matching prose would make the ordering check meaningless.
	restart := strings.Index(fn, "s.xrayService.RestartXray(true)")
	if restart < 0 {
		t.Fatal("UpdateXray no longer restarts the core, so a downloaded binary is never picked up")
	}
	download := strings.Index(fn, "versions.Install(")
	if download < 0 || download > restart {
		t.Fatal("the download does not run before the restart; it must happen while the old core is still serving, because the panel's egress proxy is hosted by it")
	}
}
