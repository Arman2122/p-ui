package xrayruntime

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/main/distro/all"
	"google.golang.org/protobuf/proto"

	"github.com/Arman2122/p-ui/internal/cores/internal/xray/enforcement"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type Runtime struct {
	instance       *core.Instance
	configuration  []byte
	structure      *core.Config
	authentication *enforcement.Authentication
}

func xrayConfig(data []byte) (*core.Config, error) {
	if _, err := configurationIdentities(data); err != nil {
		return nil, err
	}
	var input conf.Config
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, err
	}
	config, err := input.Build()
	if err != nil {
		return nil, err
	}
	if err := enforcement.Install(config); err != nil {
		return nil, err
	}
	return config, nil
}

func (*Runtime) Validate(data []byte) error {
	config, err := xrayConfig(data)
	if err != nil {
		return err
	}
	identities, err := configurationIdentities(data)
	if err != nil {
		return err
	}
	_, _, err = authenticationConfiguration(context.Background(), config, identities)
	return err
}

func (x *Runtime) Apply(ctx context.Context, data []byte, lookup func(string) (*limits.Account, bool)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := xrayConfig(data)
	if err != nil {
		return err
	}
	identities, err := configurationIdentities(data)
	if err != nil {
		return err
	}
	desired, structure, err := authenticationConfiguration(ctx, config, identities)
	if err != nil {
		return err
	}
	if x.instance != nil && x.structure != nil && structure != nil && proto.Equal(x.structure, structure) {
		if err := x.updateAuthentication(ctx, desired); err != nil {
			return err
		}
		x.configuration = bytes.Clone(data)
		return nil
	}
	if err := x.Close(); err != nil {
		return err
	}
	ctx, err = enforcement.WithClientIdentities(ctx, identities)
	if err != nil {
		return err
	}
	x.authentication = enforcement.NewAuthentication()
	ctx = enforcement.WithAuthentication(ctx, x.authentication)
	instance, err := core.NewWithContext(enforcement.WithAccounts(ctx, enforcement.Lookup(lookup)), config)
	if err != nil {
		return err
	}
	x.instance = instance
	if err := x.registerAuthentication(ctx, desired); err != nil {
		_ = x.Close()
		return err
	}
	if err := instance.Start(); err != nil {
		_ = x.Close()
		return err
	}
	x.structure, x.configuration = structure, bytes.Clone(data)
	return nil
}

func (x *Runtime) Close() error {
	if x.authentication != nil {
		x.authentication.Retire(nil)
	}
	if x.instance == nil {
		return nil
	}
	err := x.instance.Close()
	x.instance = nil
	return err
}
