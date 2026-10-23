package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/web"
)

func roleCommand(role config.Role, args []string, output io.Writer) (bool, error) {
	if len(args) == 0 || (args[0] != "role" && args[0] != "preflight") {
		return false, nil
	}
	if len(args) != 1 {
		return true, fmt.Errorf("%s takes no arguments", args[0])
	}
	if args[0] == "preflight" {
		if err := role.ValidatePanel(); err != nil {
			return true, err
		}
		if strings.TrimSpace(os.Getenv("PUI_DB_DSN")) == "" {
			return true, errors.New("PUI_DB_DSN is required for a panel role")
		}
	}
	name := string(role)
	if role == config.RoleLegacy {
		name = "legacy"
	}
	return true, json.NewEncoder(output).Encode(struct {
		Role         string `json:"role"`
		Panel        bool   `json:"panel"`
		LocalRuntime bool   `json:"localRuntime"`
	}{name, role != config.RoleNode, role == config.RoleLegacy})
}

func buildFleetRoleServices(context.Context) (web.FleetRoleServices, error) {
	return web.FleetRoleServices{}, fmt.Errorf("%w: business factory is not installed", web.ErrFleetRoleServices)
}

func roleServerOptions(ctx context.Context, role config.Role) ([]web.ServerOption, error) {
	if !role.ManagementOnly() {
		return nil, nil
	}
	services, err := buildFleetRoleServices(ctx)
	if err != nil {
		return nil, err
	}
	return []web.ServerOption{web.WithFleetRoleServices(services)}, nil
}
