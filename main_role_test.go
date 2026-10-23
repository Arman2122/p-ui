package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/config"
)

func TestRolePreflightDoesNotOpenDatabase(t *testing.T) {
	t.Setenv("PUI_DB_DSN", "postgres://invalid:1/unreachable")
	var output bytes.Buffer
	handled, err := roleCommand(config.RoleMaster, []string{"preflight"}, &output)
	if !handled || err != nil || output.String() != "{\"role\":\"master\",\"panel\":true,\"localRuntime\":false}\n" {
		t.Fatalf("read-only preflight=%t %q %v", handled, output.String(), err)
	}
	if _, err := roleCommand(config.RoleNode, []string{"preflight"}, &output); !errors.Is(err, config.ErrNodeRole) {
		t.Fatalf("node preflight accepted a panel: %v", err)
	}
	t.Setenv("PUI_DB_DSN", "")
	if _, err := roleCommand(config.RoleCombined, []string{"preflight"}, &output); err == nil || err.Error() != "PUI_DB_DSN is required for a panel role" {
		t.Fatalf("missing database configuration was accepted: %v", err)
	}
}
