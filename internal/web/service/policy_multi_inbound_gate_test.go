package service

import (
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
)

/*
The IP cap follows the client, not the lowest-id inbound.

limit_ip lives on the client row and is keyed by email alone, so the cap is a
property of the client. Enforcement was gated on hosted[0].Enable — and hosted[0]
is merely the local inbound with the smallest id, chosen by a sort order that has
nothing to do with where the traffic was seen. Disable that one while the same
client keeps connecting through a second, enabled inbound and the cap silently
stopped applying. F306.

The client below is attached to two inbounds and observed on three addresses
against a cap of one. The gate must still decide, because an enabled inbound is
carrying it.
*/
func TestTheCapStillAppliesWhenAnotherInboundIsEnabled(t *testing.T) {
	initTestDB(t)

	disabled := seedPolicyInbound(t, core.Kind("vless"), "lowest-id", 25101, "alice@e")
	// Seeded WITHOUT the email: seedPolicyInbound creates the client row, and
	// the client already exists. The link is what puts it on both inbounds.
	enabled := seedPolicyInbound(t, core.Kind("vless"), "second", 25102)
	attachClientTo(t, "alice@e", enabled.Id)
	setLimitIP(t, "alice@e", 1)

	// The lowest-id inbound goes down; the client keeps arriving on the other.
	if err := database.GetDB().Model(&model.Inbound{}).
		Where("id = ?", disabled.Id).Update("enable", false).Error; err != nil {
		t.Fatalf("disable the lowest-id inbound: %v", err)
	}

	scan := scanOf("alice@e", "198.51.100.1", "198.51.100.2", "198.51.100.3")
	verdicts, err := (&PolicyService{}).EvaluateIPLimits(scan, true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}

	banned := bannedAddresses(verdicts, "alice@e")
	if len(banned) == 0 {
		t.Fatal("the cap was not applied: a client live on a second, enabled inbound escaped it because the lowest-id one was disabled")
	}
	if len(banned) != 2 {
		t.Fatalf("a cap of 1 over 3 addresses must ban 2, got %d: %v", len(banned), banned)
	}
}

// And with every inbound carrying the client disabled, there is still nothing to
// decide. The gate is kept — only its quantifier changed — because a disabled
// inbound can still carry traffic in flight (RUNTIME-RESULTS, F335), so removing
// it entirely would start enforcing on connections that are already being torn
// down.
func TestNoEnabledInboundStillDecidesNothing(t *testing.T) {
	initTestDB(t)

	first := seedPolicyInbound(t, core.Kind("vless"), "one", 25103, "bob@e")
	second := seedPolicyInbound(t, core.Kind("vless"), "two", 25104)
	attachClientTo(t, "bob@e", second.Id)
	setLimitIP(t, "bob@e", 1)

	for _, id := range []int{first.Id, second.Id} {
		if err := database.GetDB().Model(&model.Inbound{}).
			Where("id = ?", id).Update("enable", false).Error; err != nil {
			t.Fatalf("disable inbound %d: %v", id, err)
		}
	}

	scan := scanOf("bob@e", "198.51.100.1", "198.51.100.2")
	verdicts, err := (&PolicyService{}).EvaluateIPLimits(scan, true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if banned := bannedAddresses(verdicts, "bob@e"); len(banned) != 0 {
		t.Fatalf("no inbound carrying this client is enabled; nothing should have been banned, got %v", banned)
	}
}

// attachClientTo links an already-seeded client to a second inbound, which is
// the shape the finding is about: one client, two inbounds, one cap.
func attachClientTo(t *testing.T, email string, inboundID int) {
	t.Helper()
	record := &model.ClientRecord{}
	if err := database.GetDB().Where("email = ?", email).First(record).Error; err != nil {
		t.Fatalf("find client %s: %v", email, err)
	}
	link := &model.ClientInbound{ClientId: record.Id, InboundId: inboundID}
	if err := database.GetDB().Where("client_id = ? AND inbound_id = ?", record.Id, inboundID).
		FirstOrCreate(link).Error; err != nil {
		t.Fatalf("link %s to inbound %d: %v", email, inboundID, err)
	}
}

// scanOf builds the observation set EvaluateIPLimits consumes, without a core to
// produce it: this test is about the gate, not about observation.
func scanOf(email string, addresses ...string) SessionScan {
	at := time.Now()
	observations := make([]policy.Observation, 0, len(addresses))
	for i, address := range addresses {
		// Distinct seconds so oldest-first is deterministic rather than a tie.
		observations = append(observations, policy.Observation{
			IP:                address,
			LastSeenUnixMilli: at.Add(time.Duration(i) * time.Second).UnixMilli(),
		})
	}
	return SessionScan{ByEmail: map[string][]policy.Observation{email: observations}}
}
