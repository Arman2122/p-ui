package service

import (
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// mintedCredentials names the credentials fillProtocolDefaults just generated,
// read off the client rather than restated per protocol.
func mintedCredentials(t *testing.T, c model.Client) []string {
	t.Helper()
	var out []string
	for _, field := range []struct{ name, value string }{
		{core.CredUUID, c.ID},
		{core.CredPassword, c.Password},
		{core.CredAuth, c.Auth},
		{core.CredSecret, c.Secret},
	} {
		if field.value != "" {
			out = append(out, field.name)
		}
	}
	return out
}

/*
The two halves of a client credential must agree: the panel mints it in
fillProtocolDefaults, and the client form only offers a field its core declares.

Drift is silent in both directions — a minted-but-undeclared credential is a
value the operator can never see or rotate, and it is exactly what happens when
a kind is added to a core's Kinds() and nowhere else.
*/
func TestEveryMintedCredentialIsDeclaredByItsCore(t *testing.T) {
	registry := testCores(t)
	svc := &ClientService{}

	kinds := registry.Kinds()
	if len(kinds) == 0 {
		t.Fatal("the registry serves no kinds; this test would pass without checking anything")
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			client := model.Client{Email: "a@example.com"}
			inbound := &model.Inbound{Protocol: model.Protocol(kind), Settings: `{}`}
			if err := svc.fillProtocolDefaults(&client, inbound); err != nil {
				t.Fatalf("fillProtocolDefaults(%s): %v", kind, err)
			}
			minted := mintedCredentials(t, client)
			if len(minted) == 0 {
				return
			}

			bound, ok := registry.For(kind)
			if !ok {
				t.Fatalf("kind %q is not registered", kind)
			}
			if bound.Creds == nil {
				t.Fatalf("core %q mints %v for kind %q but declares no client credentials at all, so the form cannot offer them", bound.Core.Describe().ID, minted, kind)
			}
			declared := bound.Creds.ClientCredentials(kind)
			for _, name := range minted {
				if !slices.Contains(declared, name) {
					t.Errorf("kind %q mints %q but declares %v; the form would never show the field, so the value is generated behind the operator's back", kind, name, declared)
				}
			}
		})
	}
}
