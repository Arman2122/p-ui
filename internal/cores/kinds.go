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
	// The shared map, not a fresh build: this used to construct every adapter
	// on every call, purely to read a list that is fixed at compile time.
	return kindOwners().Kinds()
}
