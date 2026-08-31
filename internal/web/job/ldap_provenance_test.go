package job

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/service"
)

/*
The auto-delete sweep takes only the clients the sync created.

Its set is "every client on this inbound whose email the directory did not
return" — which is every hand-made account on an LDAP-managed inbound: a
contractor, a founder's own client, a test identity. On the first sweep after
off-boarding, all of them were detached from every configured inbound. Silently:
the log line reads exactly like an intended removal.

Nothing recorded who created a client, so the sweep could not tell them apart.
The clients row carries an origin now, stamped by the job that created it and
read by the sweep. F372.
*/
func seedLdapInbound(t *testing.T, tag string, port int) *model.Inbound {
	t.Helper()
	ib := &model.Inbound{
		UserId: 1, Tag: tag, Enable: true, Port: port,
		Protocol: model.VLESS, Settings: `{"clients": []}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatalf("create inbound %s: %v", tag, err)
	}
	return ib
}

func attachedEmails(t *testing.T, inboundID int) map[string]struct{} {
	t.Helper()
	clients, err := (&service.ClientService{}).ListForInbound(nil, inboundID)
	if err != nil {
		t.Fatalf("ListForInbound: %v", err)
	}
	out := make(map[string]struct{}, len(clients))
	for _, c := range clients {
		out[c.Email] = struct{}{}
	}
	return out
}

func originOf(t *testing.T, email string) string {
	t.Helper()
	rec := &model.ClientRecord{}
	if err := database.GetDB().Where("email = ?", email).First(rec).Error; err != nil {
		t.Fatalf("client %s: %v", email, err)
	}
	return rec.Origin
}

// The whole point: a hand-made client the directory never heard of survives.
func TestTheSweepKeepsAClientTheSyncDidNotCreate(t *testing.T) {
	initLdapJobDB(t)
	ib := seedLdapInbound(t, "in-43001-tcp", 43001)

	j := NewLdapSyncJob()
	const staff, contractor = "staff@example.com", "contractor@example.com"
	j.createClients([]model.Client{j.buildClient(staff, 0, 0, 0)}, []int{ib.Id}, []string{ib.Tag})

	// The admin's own client, made by hand: the same path the panel's UI takes.
	if _, err := (&service.ClientService{}).Create(&service.InboundService{},
		&service.ClientCreatePayload{
			Client:     model.Client{Email: contractor, Enable: true},
			InboundIds: []int{ib.Id},
		}); err != nil {
		t.Fatalf("seed the hand-made client: %v", err)
	}
	if got := originOf(t, contractor); got != "" {
		t.Fatalf("a hand-made client was marked as created by LDAP: origin=%q", got)
	}

	// The directory returns nobody: everyone here is a deletion candidate.
	j.deleteClientsNotInLDAP(ib.Tag, map[string]struct{}{})

	remaining := attachedEmails(t, ib.Id)
	if _, ok := remaining[contractor]; !ok {
		t.Fatal("the sweep detached a client an admin made by hand; on an LDAP inbound that is every contractor, test identity and founder's own account, gone on the first off-boarding")
	}
	if _, ok := remaining[staff]; ok {
		t.Fatal("the sweep left a client the sync created and the directory no longer returns; off-boarding does not reach it")
	}
}

// And the marker is written where the sweep reads it.
func TestTheSyncMarksTheClientsItCreates(t *testing.T) {
	initLdapJobDB(t)
	ib := seedLdapInbound(t, "in-43002-tcp", 43002)

	j := NewLdapSyncJob()
	const email = "marked@example.com"
	j.createClients([]model.Client{j.buildClient(email, 0, 0, 0)}, []int{ib.Id}, []string{ib.Tag})

	if got := originOf(t, email); got != model.ClientOriginLDAP {
		t.Fatalf("origin = %q, want %q; the sweep cannot tell this client from a hand-made one", got, model.ClientOriginLDAP)
	}
}

/*
Re-adding an existing hand-made client through the sync must not adopt it.

ClientService.Create reuses a stored identity when the email is already taken,
so a client an admin made comes back through the create path the moment the
directory starts returning that address. Converting it to LDAP-owned there is
adoption -- and adoption means the next off-boarding detaches an account the
panel never created.
*/
func TestReAddingAHandMadeClientDoesNotAdoptIt(t *testing.T) {
	initLdapJobDB(t)
	ib := seedLdapInbound(t, "in-43003-tcp", 43003)

	const email = "founder@example.com"
	if _, err := (&service.ClientService{}).Create(&service.InboundService{},
		&service.ClientCreatePayload{
			Client:     model.Client{Email: email, Enable: true},
			InboundIds: []int{ib.Id},
		}); err != nil {
		t.Fatalf("seed the hand-made client: %v", err)
	}

	// The directory now returns that identity, so the sync tries to create it.
	j := NewLdapSyncJob()
	j.createClients([]model.Client{j.buildClient(email, 0, 0, 0)}, []int{ib.Id}, []string{ib.Tag})

	if got := originOf(t, email); got != "" {
		t.Fatalf("origin = %q: a client an admin made by hand became LDAP-owned, so the next off-boarding detaches it", got)
	}
}

// A client with no origin at all -- every row that predates the column -- is
// grandfathered as hand-made, which is the safe direction.
func TestAClientPredatingTheColumnIsNeverSwept(t *testing.T) {
	initLdapJobDB(t)
	ib := seedLdapInbound(t, "in-43004-tcp", 43004)

	j := NewLdapSyncJob()
	const email = "legacy@example.com"
	j.createClients([]model.Client{j.buildClient(email, 0, 0, 0)}, []int{ib.Id}, []string{ib.Tag})

	// Roll it back to the pre-migration state.
	if err := database.GetDB().Model(&model.ClientRecord{}).
		Where("email = ?", email).Update("origin", "").Error; err != nil {
		t.Fatalf("clear origin: %v", err)
	}

	j.deleteClientsNotInLDAP(ib.Tag, map[string]struct{}{})

	if _, ok := attachedEmails(t, ib.Id)[email]; !ok {
		t.Fatal("a client created before the origin column existed was swept; an upgrade would detach accounts the operator never chose to lose")
	}
}
