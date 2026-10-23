package cores

import "github.com/Arman2122/p-ui/internal/core"

// SourceClientCredentialAuthority never falls back to a host-capable legacy
// minter; an absent source capability must remain unavailable to the master.
func SourceClientCredentialAuthority(kind core.Kind) (core.SourceCredentialDeclarer, bool) {
	bound, ok := kindOwners().For(kind)
	if !ok || bound.SourceCreds == nil || len(bound.SourceCreds.SourceClientCredentials(kind)) == 0 {
		return nil, false
	}
	return bound.SourceCreds, true
}
