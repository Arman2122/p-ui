package coretest

import "testing"

// RunAdapterSuite reports every conformance failure for the core the rig builds.
// One subtest per invariant so a failure names what broke, not just that
// something did.
func RunAdapterSuite(t *testing.T, rig Rig) {
	t.Helper()
	byInvariant := map[string][]Failure{}
	order := make([]string, 0, 8)
	for _, f := range Check(rig) {
		if _, seen := byInvariant[f.Invariant]; !seen {
			order = append(order, f.Invariant)
		}
		byInvariant[f.Invariant] = append(byInvariant[f.Invariant], f)
	}
	for _, invariant := range order {
		t.Run(invariant, func(t *testing.T) {
			for _, f := range byInvariant[invariant] {
				t.Error(f.Detail)
			}
		})
	}
}
