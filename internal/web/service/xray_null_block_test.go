package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/util/json_util"
)

/*
A null block in the stored template must not kill the panel.

`json.Unmarshal` of a literal JSON `null` into a map leaves the map nil and
returns no error, so the write-back a few lines later is "assignment to entry in
nil map" -- a panic, not a fallthrough. Three functions do exactly that.

Two of them sit on the config-generation path, which Server.Start reaches
synchronously on the main goroutine. internal/web/web.go has no recover() and
main.go calls server.Start() bare, so the panic kills the process: under systemd
that is a boot crash loop with no web UI left to undo the template with. (Cron
and HTTP are safe -- cron.Recover and gin's Recovery are both wired.)

The three must be guarded together, and that is not a stylistic preference.
EnsureStatsRouting's panic is currently the ONLY thing stopping a literal-null
template from reaching the database: SaveXraySetting passes CheckXrayConfig,
then panics into gin's Recovery, and the save aborts with a 500. Guarding only
that one lets `null` persist, and the NEXT BOOT panics in ensureStatsPolicy
instead -- turning a recovered 500 into an unbootable panel. F304.
*/

func TestANullPolicyBlockDoesNotPanicTheConfigBuild(t *testing.T) {
	// Exactly what an operator gets by typing `"policy": null` in the template
	// editor, or by a tool that serialises an empty struct as null.
	got := ensureStatsPolicy(json_utilRaw(`null`))

	if string(got) != "null" {
		t.Errorf("a null policy block was rewritten to %q; it must be left exactly as stored", string(got))
	}
}

func TestANullApiBlockDoesNotPanicTheConfigBuild(t *testing.T) {
	got := ensureAPIServices(json_utilRaw(`null`))

	if string(got) != "null" {
		t.Errorf("a null api block was rewritten to %q; it must be left exactly as stored", string(got))
	}
}

func TestANullTemplateDoesNotPanicTheRoutingEnsure(t *testing.T) {
	out, err := EnsureStatsRouting(`null`)
	if err != nil {
		t.Fatalf("EnsureStatsRouting on a null template: %v", err)
	}
	if out != "null" {
		t.Errorf("a null template became %q; it must be returned unchanged", out)
	}
}

/*
And the guards must not change anything for a template that works today.

The value of this pair is the regression half: every one of these inputs takes
the same path through the same code before and after, so if any of them moves,
the guard was written in the wrong place.
*/
func TestTheNullGuardsChangeNothingElse(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) string
		in   string
		want func(string) bool
	}{
		{
			name: "a policy block still gains level 0",
			fn:   func(s string) string { return string(ensureStatsPolicy(json_utilRaw(s))) },
			in:   `{"levels":{}}`,
			want: func(out string) bool { return strings.Contains(out, `"0"`) },
		},
		{
			name: "an api block still gains the three services",
			fn:   func(s string) string { return string(ensureAPIServices(json_utilRaw(s))) },
			in:   `{"tag":"api"}`,
			want: func(out string) bool { return strings.Contains(out, "StatsService") },
		},
		{
			name: "an absent policy block is still left alone",
			fn:   func(s string) string { return string(ensureStatsPolicy(json_utilRaw(s))) },
			in:   ``,
			want: func(out string) bool { return out == "" },
		},
		{
			name: "an unparsable policy block is still left alone",
			fn:   func(s string) string { return string(ensureStatsPolicy(json_utilRaw(s))) },
			in:   `{not json`,
			want: func(out string) bool { return out == `{not json` },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if out := tc.fn(tc.in); !tc.want(out) {
				t.Errorf("got %q", out)
			}
		})
	}
}

// json_utilRaw keeps the three tests above readable; the production signature
// takes the core's RawMessage alias rather than a string.
func json_utilRaw(s string) json_util.RawMessage {
	return json_util.RawMessage(s)
}
