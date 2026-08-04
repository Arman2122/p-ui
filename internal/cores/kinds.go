package cores

import "github.com/Arman2122/p-ui/internal/core"

/*
Kinds is the panel's protocol allow-list: every kind some registered core serves.

Request validation and codegen both read it, so registering a core is the only
edit adding a protocol needs. The list used to be retyped in a `oneof=` struct
tag, and the two drifted — the tag accepted tun while no core claimed it, so the
panel stored those inbounds and then refused to apply them.
*/
func Kinds() []core.Kind {
	reg := core.NewRegistry()
	if err := Register(reg, Deps{}); err != nil {
		// Only a duplicate or empty kind fails here, and two cores claiming one
		// protocol has no correct routing. TestDefaultRegistryIsCoherent covers it.
		panic("cores: " + err.Error())
	}
	return reg.Kinds()
}
