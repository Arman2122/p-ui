package openvpn

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
disable-dco is a 2.6 directive: Ubuntu 22.04's 2.5.11 refuses it and every
inbound and exit died at start. It is written only for a daemon that has DCO.
*/
func TestDisableDCOIsWrittenForADaemonThatHasDCO(t *testing.T) {
	for version, want := range map[string]bool{"2.5.11": false, "2.6.19": true, "2.7.0": true, "3.0.1": true, "2.4.12": false, "garbage": false} {
		if got := versionAtLeast(version, 2, 6); got != want {
			t.Errorf("versionAtLeast(%q) = %v, want %v", version, got, want)
		}
	}
	s, _ := parseSettings(`{"proto":"udp","subnet":"10.9.0.0/24"}`)
	inst := core.Instance{ID: 4, Tag: "ovpn-4", Port: 1194}
	if conf := renderServer(inst, s, pathsIn("/state/4"), PluginPath, false); strings.Contains(conf, "disable-dco") {
		t.Fatalf("a 2.5 daemon was told to disable DCO:\n%s", conf)
	}
	if conf := renderServer(inst, s, pathsIn("/state/4"), PluginPath, true); !strings.Contains(conf, "\ndisable-dco\n") {
		t.Fatalf("a 2.6 daemon was not told to disable DCO:\n%s", conf)
	}
	profile, err := parseExitProfile("client\nremote vpn.example.net 1194 udp\n<ca>\nx\n</ca>\n")
	if err != nil {
		t.Fatal(err)
	}
	if conf := renderExitConfig(7, profile, "", false); strings.Contains(conf, "disable-dco") {
		t.Fatalf("a 2.5 client was told to disable DCO:\n%s", conf)
	}
}
