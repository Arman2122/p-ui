package sub

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The user the subscription hands a core for rendering must carry every scalar
credential the vocabulary names, or the core sees "" and renders nothing: an
SSH client's subscription was 404 for exactly that. Pure, so it runs everywhere.
*/
func TestShareUserCarriesEveryScalarCredential(t *testing.T) {
	psk := "psk-value"
	client := model.Client{
		ID: "uuid-value", Username: "username-value", Password: "password-value", Auth: "auth-value",
		Security: "security-value", Secret: "secret-value", AdTag: "adTag-value",
		PrivateKey: "privateKey-value", PublicKey: "publicKey-value", PreSharedKey: &psk,
		AllowedIPs: []string{"10.0.0.2/32"}, Email: "a@x",
	}
	got := userForShare(client).Credentials
	for _, name := range core.ClientCredentialNames() {
		if name == core.CredAllowedIPs {
			if list, ok := got[name].([]any); !ok || len(list) != 1 {
				t.Errorf("%s = %v, want the one prefix", name, got[name])
			}
			continue
		}
		want := name + "-value"
		if name == core.CredPreSharedKey {
			want = psk
		}
		if got[name] != want {
			t.Errorf("credential %q reaches the core as %v, want %q", name, got[name], want)
		}
	}
}
