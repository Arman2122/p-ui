package cores

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The validator tag is what the panel actually accepts, so it — not the Go const
block — is the set the registry has to cover.

Since runtime.Local resolves an inbound's core by protocol, a value the tag
admits but no core claims is an inbound the panel stores and then refuses to
serve. That is how "tun" broke: it has no model constant, so the guard that
compares kinds against the constants saw nothing wrong, while the frontend went
on offering it and xray-core went on implementing it.
*/

var oneofValues = regexp.MustCompile(`oneof=([a-z0-9 ]+)`)

func acceptedProtocols(t *testing.T) []string {
	t.Helper()
	field, ok := reflect.TypeOf(model.Inbound{}).FieldByName("Protocol")
	if !ok {
		t.Fatal("model.Inbound has no Protocol field; this guard is looking at the wrong struct")
	}
	match := oneofValues.FindStringSubmatch(field.Tag.Get("validate"))
	if match == nil {
		t.Fatalf("Inbound.Protocol has no oneof= in %q; the allow-list moved and this guard is vacuous", field.Tag.Get("validate"))
	}
	values := strings.Fields(match[1])
	if len(values) == 0 {
		t.Fatal("the oneof= list is empty; this guard is certifying nothing")
	}
	return values
}

func TestEveryAcceptedProtocolHasACore(t *testing.T) {
	registry, err := Default(Deps{})
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	for _, protocol := range acceptedProtocols(t) {
		if _, ok := registry.For(core.Kind(protocol)); !ok {
			t.Errorf("the validator accepts protocol %q but no registered core serves it — the panel would store that inbound and then refuse to apply it; either register a core for it or stop accepting it", protocol)
		}
	}
}

// The reverse direction: a core claiming a kind the panel will not accept is
// dead code at best, and at worst a kind whose validator entry was dropped.
func TestEveryRegisteredKindIsAccepted(t *testing.T) {
	registry, err := Default(Deps{})
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	accepted := make(map[string]bool)
	for _, protocol := range acceptedProtocols(t) {
		accepted[protocol] = true
	}
	for _, kind := range registry.Kinds() {
		if !accepted[string(kind)] {
			t.Errorf("core kind %q is registered but the validator rejects it, so no inbound can ever reach it", kind)
		}
	}
}
