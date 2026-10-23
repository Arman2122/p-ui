package nativeruntime

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/native"
)

func TestNativeAuthorityRequiresCurrentConfiguredCredentialProof(t *testing.T) {
	config, descriptors := nativeAuthenticationConfig(t)
	runtime := asRuntime(t, config)
	runtime.Admissions = descriptors
	compiled, err := compile(runtime)
	if err != nil {
		t.Fatal(err)
	}
	broker := admission.New("native", func() (time.Time, error) { return time.Now(), nil })
	binding, err := broker.Prepare(descriptors)
	if err != nil {
		t.Fatal(err)
	}
	_, proofs, err := captureNativeAuthentication(admission.WithBinding(t.Context(), binding), runtime)
	if err != nil || broker.Commit(1, binding) != nil {
		t.Fatalf("native proof publication: %v", err)
	}
	updates := []authority.PolicyUpdate{}
	for _, descriptor := range descriptors {
		updates = append(updates, authority.PolicyUpdate{GrantID: uuid.NewString(), ClientID: descriptor.ClientID, Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, AuthorityRevision: 1, CreditRevision: 1, Enabled: true, CreditCeiling: 1000, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}})
	}
	assignments, err := nativeAuthorityAssignments(updates, compiled.selectors, proofs)
	if err != nil || len(assignments) != 2 || len(assignments[0].Selectors) != 1 || len(assignments[1].Selectors) != 0 || len(compiled.assignments) != 0 {
		t.Fatalf("authority lost configured selector scope: %+v %v", assignments, err)
	}
	if _, err := nativeAuthorityAssignments(updates, compiled.selectors, nil); !errors.Is(err, native.ErrAuthority) {
		t.Fatalf("unproved native identity accepted authority: %v", err)
	}
	foreign := updates[0]
	foreign.ClientID = uuid.NewString()
	if _, err := nativeAuthorityAssignments([]authority.PolicyUpdate{foreign}, compiled.selectors, proofs); !errors.Is(err, native.ErrAuthority) {
		t.Fatalf("unconfigured client accepted authority: %v", err)
	}
	descriptors[0].CredentialGeneration++
	if _, err := broker.Prepare(descriptors); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeAuthorityAssignments(updates[:1], compiled.selectors, proofs); !errors.Is(err, native.ErrAuthority) {
		t.Fatalf("retired credential proof accepted authority: %v", err)
	}
	if _, err := nativeAuthorityAssignments(updates[1:], compiled.selectors, proofs); err != nil {
		t.Fatalf("retiring A invalidated unchanged B: %v", err)
	}
}
