package admission

import "slices"

// Entries require authenticated hook, race, and negative-control qualification before publication.
var qualifiedRuntimes = []string{"xray", "ssh", "singbox", "mtproto"}

func SupportsRuntime(core string) bool {
	return slices.Contains(qualifiedRuntimes, core)
}
