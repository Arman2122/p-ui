package service

import (
	"encoding/json"
	"strings"
	"testing"

	json_util "github.com/Arman2122/p-ui/internal/util/json_util"
)

/*
Two ways the generated config could be wrong in a way nobody would see.

F112 — per-user traffic counters. xray-core registers them only when the user's
POLICY LEVEL carries statsUserUplink and statsUserDownlink; with the flag off it
never calls GetOrRegisterCounter, so QueryStats returns no
`user>>>…>>>traffic>>>…` row at all. The panel then reads absence, not zero:
quotas never deplete and every traffic chart is flat, with nothing in any log.

ensureStatsPolicy guaranteed only statsUserOnline. Worse, it MANUFACTURED the
broken state: given a policy block with no level "0" it inserts an empty one and
then set only that flag. The stock config.json carries uplink and downlink but
not online, which is why the function exists at all — so neither side can be
assumed and all three have to be forced.
*/

func levelsOf(t *testing.T, out json_util.RawMessage) map[string]map[string]any {
	t.Helper()
	var parsed struct {
		Levels map[string]map[string]any `json:"levels"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("result is not a policy object: %v\n%s", err, out)
	}
	return parsed.Levels
}

func TestEveryPolicyLevelCountsPerUserTraffic(t *testing.T) {
	cases := []struct {
		name   string
		policy string
		why    string
	}{
		{
			"a policy block with no level 0 at all",
			`{"system":{"statsInboundUplink":true}}`,
			"this function inserts the level, so it is the one manufacturing the broken state",
		},
		{
			"a level 0 that only tweaks a timeout",
			`{"levels":{"0":{"handshake":4}}}`,
			"an operator tuning a timeout must not silently switch accounting off",
		},
		{
			"the stock shape, which has traffic but not online",
			`{"levels":{"0":{"statsUserUplink":true,"statsUserDownlink":true}}}`,
			"the case ensureStatsPolicy was written for",
		},
		{
			"a second level beside 0",
			`{"levels":{"0":{},"1":{"handshake":4}}}`,
			"a client on a non-zero level is accounted by that level, not by level 0",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			levels := levelsOf(t, ensureStatsPolicy(json_util.RawMessage(c.policy)))
			if len(levels) == 0 {
				t.Fatalf("no levels at all (%s)", c.why)
			}
			for name, level := range levels {
				for _, flag := range []string{"statsUserOnline", "statsUserUplink", "statsUserDownlink"} {
					if on, ok := level[flag].(bool); !ok || !on {
						t.Errorf("level %q has %s = %v; xray-core then never registers the counter, so the panel reads absence and quotas never deplete (%s)",
							name, flag, level[flag], c.why)
					}
				}
			}
		})
	}
}

// An already-correct policy must come back byte-identical: the changed gate is
// what stops a spurious config difference triggering a restart every tick.
func TestAnAlreadyCorrectPolicyIsNotRewritten(t *testing.T) {
	in := json_util.RawMessage(`{"levels":{"0":{"statsUserOnline":true,"statsUserUplink":true,"statsUserDownlink":true}}}`)
	out := ensureStatsPolicy(in)
	if string(out) != string(in) {
		t.Fatalf("a correct policy was rewritten:\n got: %s\nwant: %s", out, in)
	}
}

// And an empty policy is still left alone — a template with no policy key falls
// through to xray-core's own defaults, which is a separate decision.
func TestAnEmptyPolicyIsLeftAlone(t *testing.T) {
	if got := ensureStatsPolicy(json_util.RawMessage("")); string(got) != "" {
		t.Fatalf("an empty policy became %q", got)
	}
}

/*
F055 — the config must not be generated at all when the operator's routing rules
could not be compiled.

A config missing every rule is perfectly valid to xray-core, so the restart
SUCCEEDS, the need-restart flag is consumed and never re-armed, and the panel
looks healthy. What actually happens is that L3 ingresses go dark and Xray
clients fall through to the template — out of this server's own address, which
is the opposite of what an uplink rule was written for.

This is a source guard rather than a behavioural one: driving it needs a
database, a core and a compile failure, none of which decides the question. What
decides it is whether the error reaches the caller.
*/
func TestTheConfigBuildRefusesToDropTheRoutingPlan(t *testing.T) {
	body := readServiceSource(t, "xray.go")

	start := strings.Index(body, "func (s *XrayService) GetXrayConfig(")
	if start < 0 {
		t.Fatal("GetXrayConfig was renamed; this guard is now vacuous")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of GetXrayConfig; this guard is now vacuous")
	}
	fn := body[start : start+end]

	call := strings.Index(fn, "applyRoutingPlan(")
	if call < 0 {
		t.Fatal("GetXrayConfig no longer applies the routing plan at all")
	}
	tail := fn[call:]
	stop := strings.Index(tail, "\n\t}")
	if stop < 0 {
		t.Fatal("cannot read the applyRoutingPlan error branch")
	}
	branch := tail[:stop]

	if !strings.Contains(branch, "return nil,") {
		t.Fatalf("applyRoutingPlan's error does not reach the caller, so a config with none of the operator's rules is deployed and the restart reports success:\n%s", branch)
	}
}

// reapFronts is the one error on that path that must NOT refuse the config:
// it is cleanup, and a failed retire can only orphan the one ingress whose row
// survived. Hard-failing it would darken every inbound on the box to tidy up one.
func TestRetiringAFrontCannotBlockTheConfig(t *testing.T) {
	body := readServiceSource(t, "routing.go")

	call := strings.Index(body, "s.reapFronts(out)")
	if call < 0 {
		t.Fatal("reapFronts is no longer called from ensureFronts; this guard is now vacuous")
	}
	window := body[maxInt(0, call-160) : call+160]

	if strings.Contains(window, "return out, s.reapFronts(out)") {
		t.Fatal("a failed front retire refuses the whole config, so one stale egress row stops Xray starting at all")
	}
	if !strings.Contains(window, "logger.Warning") {
		t.Fatalf("a failed retire is neither returned nor reported:\n%s", window)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
