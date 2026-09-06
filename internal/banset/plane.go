package banset

import "context"

// Plane is the host firewall as this manager uses it, an interface so
// convergence is table-testable off Linux. It deals in scripts only.
type Plane interface {
	// Probe reports whether this host's firewall can be written by this panel at all.
	Probe(ctx context.Context) error

	// Apply runs one nft script as one transaction: all of it lands or none does.
	Apply(ctx context.Context, script string) error
}
