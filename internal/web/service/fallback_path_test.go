package service

import (
	"strings"
	"testing"
)

/*
A fallback path without a leading slash fails the whole config.

xray-core refuses it outright — `"path" must be empty or start with "/"`, at
infra/conf/vless.go:185 and trojan.go:175 — and that refusal fails the ENTIRE
config, so every inbound of every protocol stops serving, not just this one.

It is not a typo an operator has to make. The Fallbacks card fills the path in
by itself from the child's transport, and for a gRPC child it filled in the bare
`serviceName` — no slash. Two clicks, and the box does not come back up on its
next cold start. F326.

The `xray -test` pre-flight added earlier this campaign catches this at a
RESTART and refuses to stop the running process, so the surviving window is a
cold start: a reboot, or a first start after the row was written. This guard
stops the row being written at all.
*/
func TestAFallbackPathMustStartWithASlash(t *testing.T) {
	setupConflictDB(t)
	master := seedFallbackMaster(t, "fb-path", 25131, "none")

	cases := []struct {
		name    string
		path    string
		refused bool
		why     string
	}{
		{
			name:    "a gRPC serviceName as the form fills it in",
			path:    "GunService",
			refused: true,
			why:     "this is the value deriveFallbackDefaults used to write for a gRPC child",
		},
		{
			name:    "the same name with the slash the core wants",
			path:    "/GunService",
			refused: false,
			why:     "the gRPC request path is /{serviceName}/{method}",
		},
		{
			name:    "a websocket path, which already carries one",
			path:    "/ws",
			refused: false,
			why:     "wsSettings.path is stored with its slash",
		},
		{
			name:    "no path at all",
			path:    "",
			refused: false,
			why:     "empty is explicitly allowed by the core",
		},
		{
			name:    "whitespace around a slashless path is still slashless",
			path:    "  GunService  ",
			refused: true,
			why:     "trimming must not be a way past the guard",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := (&FallbackService{}).SetByMaster(master.Id, []FallbackInput{{Dest: "8080", Path: c.path}})
			if c.refused {
				if err == nil {
					t.Fatalf("stored a path xray-core refuses, and the refusal fails the whole config (%s)", c.why)
				}
				if !strings.Contains(err.Error(), `start with "/"`) {
					t.Fatalf("the refusal should say what shape is wanted, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a path the core accepts (%s): %v", c.why, err)
			}
		})
	}
}
