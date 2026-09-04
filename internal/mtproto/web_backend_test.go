package mtproto

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Telegram Desktop 7.1's "WEB proxy" front-end (tproxy-server) speaks plain TCP
obfuscated2 to a loopback backend and accepts only the two pre-FakeTLS secret
forms, 16-byte plain and 17-byte 0xdd — never ee. mtg-proxy serves those on
their own [[listener]] sockets, so the panel opts one in per inbound.

The secret itself does not change: a mode is a property of the listening socket,
not of the key, so the same 16 bytes answer as ee on bind-to and as dd/plain on
the loopback listener. The panel keeps minting and storing canonical ee, and
UsableSecret keeps requiring 0xee (pinned by
TestUsableSecretMirrorsWhatTheSidecarAccepts, whose "a bare 32-hex secret" and
"a dd-prefixed obfuscated2 secret" rows are exactly this invariant).

An mtg-proxy too old for [[listener]] ignores unknown TOML keys, so emitting the
block against one is harmless and needs no capability gate — which is what lets
the panel roll ahead of the binary.
*/

// webBackendBlock is the exact TOML the emission adds. Kept as one literal so
// the equivalence assertions below cannot drift from the renderer.
const webBackendBlock = "\n[[listener]]\nbind-to = \"127.0.0.1:2398\"\naccept = [\"dd\", \"plain\"]\n"

// webBackendInstance carries a throttle section and a tagged client, so both
// ordering bounds around the [[listener]] block actually exist.
func webBackendInstance() Instance {
	return Instance{
		Listen: "0.0.0.0", Port: 443,
		Secrets: []SecretEntry{
			{Name: "alice", Secret: "ee11", AdTag: "0123456789abcdef0123456789abcdef"},
			{Name: "bob", Secret: "ee22", QuotaBytes: 1 << 30},
		},
		ThrottleMaxConnections: 5000,
	}
}

func TestRenderConfigWebBackendIsPurelyAdditive(t *testing.T) {
	base := webBackendInstance()
	enabled := base
	enabled.WebBackendEnable = true
	enabled.WebBackendPort = 2398

	off := renderConfig(base, 6000, "sesame")
	on := renderConfig(enabled, 6000, "sesame")

	if got := strings.Count(on, webBackendBlock); got != 1 {
		t.Fatalf("enabling the WEB backend must emit exactly one %q block, got %d:\n%s", "[[listener]]", got, on)
	}
	// Byte for byte: an inbound that never opts in must keep rendering exactly
	// what it renders today, or every one of them restarts on upgrade.
	if stripped := strings.Replace(on, webBackendBlock, "", 1); stripped != off {
		t.Fatalf("enabling the WEB backend changed something other than the listener block:\nwith:\n%s\nwithout:\n%s", stripped, off)
	}

	t.Run("halfConfigured", func(t *testing.T) {
		// A toggle with no allocated port has nothing to bind, and a stale port
		// with the toggle off is not an opt-in. Neither may emit a listener.
		for name, inst := range map[string]Instance{
			"enabledWithoutPort": {Listen: "0.0.0.0", Port: 443, Secrets: base.Secrets, WebBackendEnable: true},
			"portWithoutEnable":  {Listen: "0.0.0.0", Port: 443, Secrets: base.Secrets, WebBackendPort: 2398},
		} {
			t.Run(name, func(t *testing.T) {
				got := renderConfig(inst, 6000, "sesame")
				if strings.Contains(got, "[[listener]]") {
					t.Fatalf("%s must emit no listener:\n%s", name, got)
				}
			})
		}
	})
}

func TestRenderConfigWebBackendPosition(t *testing.T) {
	full := webBackendInstance()
	full.WebBackendEnable = true
	full.WebBackendPort = 2398
	cfg := renderConfig(full, 6000, "sesame")

	at := strings.Index(cfg, "[[listener]]")
	if at < 0 {
		t.Fatalf("no listener block emitted:\n%s", cfg)
	}
	// An array of tables placed after [secret-ad-tags] or [secrets] would swallow
	// the keys of whichever table precedes it, so both bounds are load-bearing.
	if at < strings.Index(cfg, "[throttle]") {
		t.Fatalf("[[listener]] must follow [throttle]:\n%s", cfg)
	}
	if at > strings.Index(cfg, "[secret-ad-tags]") {
		t.Fatalf("[[listener]] must precede [secret-ad-tags]:\n%s", cfg)
	}
	if at > strings.Index(cfg, "[secret-limits.\"bob\"]") {
		t.Fatalf("[[listener]] must precede [secret-limits]:\n%s", cfg)
	}
	if at > strings.LastIndex(cfg, "[secrets]") {
		t.Fatalf("[secrets] must stay the final section:\n%s", cfg)
	}

	t.Run("noOptionalSections", func(t *testing.T) {
		// The position cannot depend on an optional section happening to be
		// there, so pin it again with throttle, ad tags and limits all absent.
		bare := renderConfig(Instance{
			Listen: "0.0.0.0", Port: 443,
			Secrets:          []SecretEntry{{Name: "alice", Secret: "ee11"}},
			WebBackendEnable: true, WebBackendPort: 2398,
		}, 6000, "")
		at := strings.Index(bare, "[[listener]]")
		if at < 0 {
			t.Fatalf("no listener block emitted:\n%s", bare)
		}
		if at < strings.Index(bare, "api-bind-to") {
			t.Fatalf("top-level keys must precede [[listener]]:\n%s", bare)
		}
		if at > strings.Index(bare, "[secrets]") {
			t.Fatalf("[[listener]] must precede [secrets]:\n%s", bare)
		}
	})
}

// A listener is not part of [secrets] and PUT /secrets carries no field for it,
// so a toggle must take the restart branch, never the hot-apply one.
func TestWebBackendTogglesForceRestart(t *testing.T) {
	base := Instance{Secrets: []SecretEntry{{Name: "a", Secret: "ee"}}, Listen: "0.0.0.0", Port: 443}
	for name, mutate := range map[string]func(*Instance){
		"enable": func(i *Instance) { i.WebBackendEnable = true },
		"port":   func(i *Instance) { i.WebBackendPort = 2398 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			got := ensureActionFor(true, base.StructuralFingerprint(), base.SecretsFingerprint(),
				changed.StructuralFingerprint(), changed.SecretsFingerprint())
			if got != ensureRestart {
				t.Fatalf("webBackend %s must restart the sidecar, got action %d: a new listener cannot be hot-applied", name, got)
			}
		})
	}
}

// Both ingest paths read the same settings JSON and must not drift: a value
// only one of them sees yields a fingerprint that picks the wrong action.
func TestWebBackendSettingsReachBothIngestPaths(t *testing.T) {
	const settings = `{"webBackendEnable":true,"webBackendPort":2398,` +
		`"clients":[{"email":"a@e","secret":"ee00112233445566778899aabbccddeeff676f6f642e6578616d706c65","enable":true}]}`

	t.Run("InstanceFromInbound", func(t *testing.T) {
		inst, ok := InstanceFromInbound(&model.Inbound{
			Id: 7, Tag: "mt", Listen: "0.0.0.0", Port: 443, Protocol: model.MTProto, Settings: settings,
		})
		if !ok {
			t.Fatal("the inbound produced no instance")
		}
		if !inst.WebBackendEnable || inst.WebBackendPort != 2398 {
			t.Fatalf("webBackend settings lost: enable=%v port=%d", inst.WebBackendEnable, inst.WebBackendPort)
		}
	})

	t.Run("ApplySettings", func(t *testing.T) {
		var inst Instance
		if err := inst.ApplySettings(settings); err != nil {
			t.Fatalf("ApplySettings: %v", err)
		}
		if !inst.WebBackendEnable || inst.WebBackendPort != 2398 {
			t.Fatalf("webBackend settings lost: enable=%v port=%d", inst.WebBackendEnable, inst.WebBackendPort)
		}
	})

	t.Run("absentMeansOff", func(t *testing.T) {
		var inst Instance
		if err := inst.ApplySettings(`{"clients":[]}`); err != nil {
			t.Fatalf("ApplySettings: %v", err)
		}
		if inst.WebBackendEnable || inst.WebBackendPort != 0 {
			t.Fatalf("an inbound that never opted in must stay off: enable=%v port=%d", inst.WebBackendEnable, inst.WebBackendPort)
		}
	})
}

// The panel writes the config before the process exists, so /stats can never
// gate emission; features is scraped to report an old binary, not to gate one.
func TestCollectTrafficRecordsSidecarFeatures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stats" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"started_at":"2026-01-01T00:00:00Z",`+
			`"features":["faketls","listeners","quota-reset","secret-modes","secrets-api"],`+
			`"listeners":[{"bind":"127.0.0.1:2398","accept":["dd","plain"],"public":false}],`+
			`"users":{"alice":{"connections":1,"bytes_in":100,"bytes_out":200}}}`)
	}))
	defer srv.Close()

	mgr := &Manager{procs: map[int]*managed{}, swept: true}
	mgr.procs[9] = &managed{
		proc: &Process{cmd: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}},
		tag:  "mtp-9", apiPort: serverPort(t, srv), counter: core.NewCounter(),
	}

	if _, _, failed := mgr.CollectTraffic(); len(failed) != 0 {
		t.Fatalf("scrape failed: %v", failed)
	}
	want := []string{"faketls", "listeners", "quota-reset", "secret-modes", "secrets-api"}
	if got := mgr.Features(9); !slices.Equal(got, want) {
		t.Fatalf("Features(9) = %v, want %v", got, want)
	}
	if got := mgr.Features(404); got != nil {
		t.Fatalf("Features of an unmanaged inbound = %v, want nil", got)
	}
}
