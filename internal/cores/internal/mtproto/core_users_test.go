package mtproto

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
AddUser and RemoveUser here rewrite the whole [secrets] section, so the caller
must hand them Instance.Users as the row now stands.

Nothing but the declaration asks for that, and the method that carries it has no
compile-time caller: drop it and every test still passes while a client added
between the caller's read and its write is silently revoked — the bug that held
up routing user ops through the registry in the first place.
*/
func TestUserOpsDeclareTheyRewriteTheWholeSet(t *testing.T) {
	bound := core.Bind(&Core{})
	if bound.Users == nil {
		t.Fatal("mtproto no longer provisions users at all")
	}
	if bound.UserSet == nil {
		t.Error("mtproto does not declare core.WholeSetUserProvisioner, so Local will apply a user op against the caller's copy of the inbound and mtg will revoke every client that copy predates")
	}
}
