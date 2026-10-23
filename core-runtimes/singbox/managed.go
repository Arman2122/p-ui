package singbox

import (
	"bytes"
	"context"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/option"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type Managed struct {
	instance       *box.Box
	configuration  []byte
	structure      []byte
	authentication *authenticationRegistry
}

func (*Managed) Validate(data []byte) error {
	var options option.Options
	if err := options.UnmarshalJSONContext(Context(context.Background()), data); err != nil {
		return err
	}
	_, _, err := managedAuthentication(data, options)
	return err
}

func (m *Managed) Apply(ctx context.Context, data []byte, lookup func(string) (*limits.Account, bool)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var options option.Options
	if err := options.UnmarshalJSONContext(Context(ctx), data); err != nil {
		return err
	}
	desired, structure, err := managedAuthentication(data, options)
	if err != nil {
		return err
	}
	if m.instance != nil && bytes.Equal(m.structure, structure) {
		if err := m.applyAuthentication(ctx, desired, false); err != nil {
			return err
		}
		m.configuration = bytes.Clone(data)
		return nil
	}
	if err := m.Close(); err != nil {
		return err
	}
	m.authentication = newAuthenticationRegistry()
	ctx = context.WithValue(ctx, authenticationKey{}, m.authentication)
	instance, err := New(ctx, options, Lookup(lookup))
	if err != nil {
		return err
	}
	m.instance = instance
	if err := m.applyAuthentication(ctx, desired, true); err != nil {
		_ = m.Close()
		return err
	}
	if err := instance.Start(); err != nil {
		_ = m.Close()
		return err
	}
	m.structure, m.configuration = structure, bytes.Clone(data)
	return nil
}

func (m *Managed) Close() error {
	if m.authentication != nil {
		m.authentication.invalidate(nil)
	}
	if m.instance == nil {
		return nil
	}
	err := m.instance.Close()
	m.instance = nil
	return err
}
