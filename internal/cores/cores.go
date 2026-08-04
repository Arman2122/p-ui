// Package cores is the one place every protocol core is wired into a registry.
package cores

import (
	"fmt"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores/internal/mtproto"
	"github.com/Arman2122/p-ui/internal/cores/internal/xray"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

// Deps are the panel-side facts a core cannot derive for itself. They are passed
// in rather than reached for, so a core still cannot import the web layer.
type Deps struct {
	// XrayBaseConfig returns the Xray config with every section except inbounds
	// filled in: routing, DNS and policy stay the panel's business.
	XrayBaseConfig func() (*engine.Config, error)
}

/*
Adding a core is one import and one Register line in this file.

Registration is explicit rather than init()-based on purpose. With init(), the
registered set is a function of the transitive import graph of whichever main
links the package — so a dropped blank import silently shrinks the generated
frontend schema while `make gen-check` still passes. It also keeps "which cores
exist" answerable by reading one file, and makes adding a core a reviewable
one-line diff.

Concrete cores live under internal/cores/internal/, so the Go compiler — not a
lint rule — stops the service layer from importing one directly.
*/

// Register wires every core into reg. Cores land here as they are ported:
// mtproto first, because it is the smaller contract and proves the interface.
func Register(reg *core.Registry, deps Deps) error {
	if reg == nil {
		return fmt.Errorf("cores: Register(nil registry)")
	}
	if err := reg.Register(mtproto.New()); err != nil {
		return err
	}
	return reg.Register(xray.New(xray.Deps{BaseConfig: deps.XrayBaseConfig}))
}

// Default builds the registry the panel runs with.
func Default(deps Deps) (*core.Registry, error) {
	reg := core.NewRegistry()
	if err := Register(reg, deps); err != nil {
		return nil, err
	}
	return reg, nil
}
