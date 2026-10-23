//go:build linux

package nativeruntime

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	ocserv "github.com/Arman2122/p-ui/internal/cores/internal/ocserv"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/native"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/radius"
)

type authorityAdapterPlane struct {
	counts  map[string]native.Counters
	leases  map[string]native.Lease
	changes int
}

func authorityLeaseName(id string) string { return "s" + strings.ReplaceAll(id, "-", "") }

func (p *authorityAdapterPlane) Install(_ context.Context, lease native.Lease) error {
	p.counts[authorityLeaseName(lease.ID)] = native.Counters{}
	p.leases[lease.ID] = lease
	return nil
}

func (p *authorityAdapterPlane) Snapshot(context.Context) (map[string]native.Counters, error) {
	return maps.Clone(p.counts), nil
}

func (p *authorityAdapterPlane) Block(_ context.Context, lease native.Lease) (native.Counters, error) {
	count, ok := p.counts[authorityLeaseName(lease.ID)]
	if !ok {
		return native.Counters{}, native.ErrMissing
	}
	return count, nil
}

func (p *authorityAdapterPlane) Stop(ctx context.Context, lease native.Lease) (native.Counters, error) {
	return p.Block(ctx, lease)
}

func (p *authorityAdapterPlane) Remove(_ context.Context, lease native.Lease) error {
	delete(p.counts, authorityLeaseName(lease.ID))
	delete(p.leases, lease.ID)
	return nil
}

func (p *authorityAdapterPlane) ReplacePolicy(_ context.Context, before, after native.Lease) error {
	if before.ID != after.ID || before.Class != after.Class || before.Bytes != after.Bytes || before.ExpiresUnixMilli != after.ExpiresUnixMilli {
		return native.ErrAuthority
	}
	p.leases[before.ID] = after
	p.changes++
	return nil
}

func TestNativeAuthorityRuntimeUpdatesAuthenticationWithoutReplacingProofs(t *testing.T) {
	directory := t.TempDir()
	usage, err := state.Open(filepath.Join(directory, "state.db"), state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = usage.Close() })
	journal, err := native.Open(filepath.Join(directory, "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	plane := &authorityAdapterPlane{counts: map[string]native.Counters{}, leases: map[string]native.Lease{}}
	control, err := native.NewController(journal, usage, "native", plane)
	if err != nil || control.Recover(t.Context()) != nil {
		t.Fatalf("controller startup: %v", err)
	}
	config, descriptors := nativeAuthenticationConfig(t)
	wanted := asRuntime(t, config)
	wanted.Admissions = descriptors
	compiled, err := compile(wanted)
	if err != nil {
		t.Fatal(err)
	}
	broker := admission.New("native", func() (time.Time, error) { return time.Now(), nil })
	binding, err := broker.Prepare(descriptors)
	if err != nil {
		t.Fatal(err)
	}
	_, proofs, err := captureNativeAuthentication(admission.WithBinding(t.Context(), binding), wanted)
	if err != nil || broker.Commit(1, binding) != nil {
		t.Fatalf("credential publication: %v", err)
	}
	r := &Runtime{configured: true, initialized: true, control: control, selectors: compiled.selectors, authentication: proofs, byClient: map[string]governor.Grant{}, openvpn: newOpenVPNManager(t.Context(), nil), ocserv: newOcservManager(t.Context(), nil)}
	authenticator := ocserv.NewNodeAuthenticator(config.OpenConnect[0])
	if err := authenticator.BindAdmission(binding, descriptors[1].InboundTag); err != nil {
		t.Fatal(err)
	}
	r.ocserv.processes[config.OpenConnect[0].ID] = &ocservProcess{auth: authenticator}
	updates := []authority.PolicyUpdate{}
	for _, descriptor := range descriptors {
		updates = append(updates, authority.PolicyUpdate{GrantID: uuid.NewString(), ClientID: descriptor.ClientID, Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, AuthorityRevision: 1, CreditRevision: 1, Enabled: true, CreditCeiling: native.MaxSliceBytes * 4, Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 800000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 800000, BurstBytes: 65536}}})
	}
	if err := r.ValidateAuthority(t.Context(), updates); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyAuthority(t.Context(), updates); err != nil {
		t.Fatal(err)
	}
	if len(r.byClient) != 2 || len(r.assignments) != 2 || len(plane.leases) != 1 || !authenticator.Authenticate(t.Context(), "bob", "valid-password", radius.Peer{}).Accept {
		t.Fatal("installed authority did not update credentials or fabricated dormant selectors")
	}
	neighbor := r.byClient[updates[1].ClientID]
	credit := authority.Credit{GrantID: updates[0].GrantID, ClientID: updates[0].ClientID, TermID: updates[0].Policy.Term, PolicyRevision: 1, CreditRevision: 2, CreditCeiling: updates[0].Policy.CreditCeiling + 1000}
	if err := r.ApplyCredit(t.Context(), []authority.Credit{credit}); err != nil {
		t.Fatal(err)
	}
	if r.byClient[credit.ClientID].Policy.CreditRevision != credit.CreditRevision || r.byClient[credit.ClientID].Policy.CreditCeiling != credit.CreditCeiling {
		t.Fatal("installed credit did not refresh runtime authentication authority")
	}
	updates[0].Policy.AuthorityRevision++
	updates[0].Policy.Upload.BitsPerSecond /= 2
	if err := r.ApplyAuthority(t.Context(), updates[:1]); err != nil {
		t.Fatal(err)
	}
	if r.byClient[updates[0].ClientID].Policy.CreditRevision != 2 || r.byClient[updates[1].ClientID] != neighbor || plane.changes != 1 || !maps.Equal(r.authentication, proofs) || !authenticator.Authenticate(t.Context(), "bob", "valid-password", radius.Peer{}).Accept {
		t.Fatal("amendment lost credit, credential proof or unrelated authentication")
	}
	descriptors[0].CredentialGeneration++
	if _, err := broker.Prepare(descriptors); err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateAuthority(t.Context(), updates[:1]); !errors.Is(err, native.ErrAuthority) {
		t.Fatalf("retired native credential accepted authority: %v", err)
	}
}
