package configuration

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/admission"
)

func TestDescriptorScopeBindsCompleteCanonicalAssignment(t *testing.T) {
	client := uuid.NewString()
	first := admission.Descriptor{ClientID: client, CredentialGeneration: 7, PlacementID: uuid.NewString(), PlacementRevision: 11, InboundInstanceID: uuid.NewString(), InboundTag: "first"}
	second := first
	second.InboundInstanceID, second.InboundTag = uuid.NewString(), "second"
	runtime := Runtime{ID: "xray", Admissions: []admission.Descriptor{first, second}}
	baseline, err := DescriptorScopes(runtime)
	if err != nil {
		t.Fatal(err)
	}
	want := baseline[client].SHA256
	reordered := runtime
	reordered.Admissions = slices.Clone(runtime.Admissions)
	slices.Reverse(reordered.Admissions)
	actual, err := DescriptorScopes(reordered)
	if err != nil || !bytes.Equal(want, actual[client].SHA256) {
		t.Fatal("descriptor order changed canonical authorization scope")
	}
	for _, test := range []struct {
		name    string
		mutate  func(*Runtime)
		invalid bool
	}{
		{"missing inbound", func(r *Runtime) { r.Admissions = r.Admissions[:1] }, false},
		{"changed tag", func(r *Runtime) { r.Admissions[1].InboundTag = "other" }, false},
		{"other runtime", func(r *Runtime) { r.ID = "xray-other" }, false},
		{"new credential", func(r *Runtime) {
			for i := range r.Admissions {
				r.Admissions[i].CredentialGeneration++
			}
		}, false},
		{"mixed credential", func(r *Runtime) { r.Admissions[1].CredentialGeneration++ }, true},
		{"mixed placement", func(r *Runtime) { r.Admissions[1].PlacementRevision++ }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := runtime
			changed.Admissions = slices.Clone(runtime.Admissions)
			test.mutate(&changed)
			actual, err := DescriptorScopes(changed)
			if test.invalid {
				if !errors.Is(err, ErrConfiguration) {
					t.Fatalf("conflicting client descriptor scope accepted: %v", err)
				}
				return
			}
			if err != nil || bytes.Equal(want, actual[client].SHA256) {
				t.Fatalf("changed complete descriptor scope was not distinguished: %v", err)
			}
		})
	}
}
