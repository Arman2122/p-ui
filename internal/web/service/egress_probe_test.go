package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Every state where a latency number would be a lie must answer with a reason.

The dangerous one is the last: a socket carrying a mark no rule catches does not
fail. It falls through to the main table, the request succeeds, and the probe
reports the SERVER's latency and exit IP beside the uplink's name — the row
reads healthy precisely because its routing is broken. So an unprovisioned
egress must refuse, and the refusal has to survive anyone later "fixing" the
probe by removing the gate.
*/
func TestEgressProbeRefusesEveryStateItCannotMeasure(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	service := &EgressService{}

	uplinkSettings := `{"privateKey":"a","address":["10.2.0.2/32"],"publicKey":"b","endpoint":"us-sfo.example:51820"}`
	off := seedEgress(t, &model.Egress{Type: "wg-client", Remark: "switched off", Settings: uplinkSettings})
	front := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Remark: "warp front", Target: "direct"})
	unrouted := seedEgress(t, &model.Egress{Type: "wg-client", Enable: true, Remark: "never converged", Settings: uplinkSettings})

	ids := []int{off.Id, front.Id, unrouted.Id, 9999}
	results, err := service.TestEgresses(context.Background(), ids, "real")
	if err != nil {
		t.Fatalf("TestEgresses: %v", err)
	}
	if len(results) != len(ids) {
		t.Fatalf("got %d results for %d ids", len(results), len(ids))
	}

	for _, tc := range []struct {
		name  string
		index int
		want  string
	}{
		{"a disabled row", 0, ErrEgressProbeDisabled.Error()},
		{"a front", 1, ErrEgressProbeIsFront.Error()},
		{"an egress the host never converged", 2, "no rule catching its mark"},
		{"an id that is gone", 3, "no longer exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := results[tc.index]
			if got.Success {
				t.Fatalf("%s reported success with delay %dms — that number measured the host's own path", tc.name, got.Delay)
			}
			if !strings.Contains(got.Error, tc.want) {
				t.Fatalf("error = %q, want it to contain %q", got.Error, tc.want)
			}
		})
	}
}

// The row's own name comes back, because an operator matches the result against
// what the table shows them, and that is the remark rather than the id.
func TestEgressProbeResultCarriesTheOperatorsName(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	service := &EgressService{}

	row := seedEgress(t, &model.Egress{Type: "wg-client", Enable: true, Remark: "US-sfo | Surfshark"})
	results, err := service.TestEgresses(context.Background(), []int{row.Id}, "tcp")
	if err != nil {
		t.Fatalf("TestEgresses: %v", err)
	}
	if results[0].Tag != "US-sfo | Surfshark" {
		t.Fatalf("Tag = %q, want the row's remark", results[0].Tag)
	}
	// tcp is promoted: a bare dial says nothing about a peer that ignores
	// unauthenticated packets.
	if results[0].Mode != "http" {
		t.Fatalf("Mode = %q, want http", results[0].Mode)
	}
}
