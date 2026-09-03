package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The SSH core validates and mints the username credential, but the table
joining the vocabulary to model.Client never carried it: every SSH client was
refused as `username ""` however the form was filled, and a minted username
had no field to land in.
*/
func TestUsernameCredentialReachesTheCoreAndTheRecord(t *testing.T) {
	client := &model.Client{Email: "olgsfhx7lk", Username: "uassfrf1", Password: "secret-12"}
	if err := validateClientForKind(model.SSH, `{"transport":"tcp"}`, client); err != nil {
		t.Fatalf("an SSH client with username %q was refused: %v", client.Username, err)
	}
	minted := &model.Client{}
	setClientCredential(minted, core.CredUsername, "u7abcdef")
	if minted.Username != "u7abcdef" {
		t.Fatalf("a minted username landed nowhere: %+v", minted)
	}
}
