package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Every credential name in the vocabulary has to be carried by the record's
tables both ways, or a core validates "" whatever the form sent: that is how
the SSH username was lost on create and again on every read. A name a table
does not carry fails here, before a core can be built on it.
*/
func TestEveryCredentialNameIsCarriedByTheClientRecord(t *testing.T) {
	for _, name := range core.ClientCredentialNames() {
		if name == core.CredAllowedIPs {
			// A list, carried by its own field; no mint, validation or identity reads it.
			continue
		}
		t.Run(name, func(t *testing.T) {
			c := &model.Client{}
			setClientCredential(c, name, "value-"+name)
			if got := clientCredentialValues(c)[name]; got != "value-"+name {
				t.Fatalf("set then get gave %q: the vocabulary and model.Client disagree about %q", got, name)
			}
		})
	}
}
