package config

import (
	"errors"
	"fmt"
	"os"
)

type Role string

const (
	RoleLegacy   Role = ""
	RoleMaster   Role = "master"
	RoleNode     Role = "node"
	RoleCombined Role = "combined"
)

var (
	ErrInvalidRole = errors.New("PUI_ROLE must be master, node, or combined")
	ErrNodeRole    = errors.New("the node role runs penhoon-agent; p-ui cannot start a panel or open its database")
)

func RoleFromEnv() (Role, error) {
	role := Role(os.Getenv("PUI_ROLE"))
	switch role {
	case RoleLegacy, RoleMaster, RoleNode, RoleCombined:
		return role, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidRole, role)
	}
}

func (r Role) ManagementOnly() bool {
	return r == RoleMaster || r == RoleCombined
}

func (r Role) ValidatePanel() error {
	switch r {
	case RoleLegacy, RoleMaster, RoleCombined:
		return nil
	case RoleNode:
		return ErrNodeRole
	default:
		return ErrInvalidRole
	}
}
