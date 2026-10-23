package config

import (
	"errors"
	"testing"
)

func TestRoleValidationRejectsUnknownAndNodePanels(t *testing.T) {
	for _, value := range []string{"MASTER", " master", "master ", "local", "master,node"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("PUI_ROLE", value)
			if _, err := RoleFromEnv(); !errors.Is(err, ErrInvalidRole) {
				t.Fatalf("unknown role accepted: %v", err)
			}
		})
	}
	t.Setenv("PUI_ROLE", "node")
	role, err := RoleFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := role.ValidatePanel(); !errors.Is(err, ErrNodeRole) {
		t.Fatalf("node role accepted panel startup: %v", err)
	}
}
