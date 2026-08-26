package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Deleting an inbound must take its per-inbound rows with it.

DelInbound removed the inbound and its hosts, and left client_credentials and
inbound_fallbacks behind. That would be dead weight if ids were never reused —
but resyncPostgresSequences deliberately rewinds the sequence after a delete, so
a NEW inbound can be created on the id the old one had. It then inherits the
dead inbound's stored credentials, and fallbacks pointing at something that no
longer exists. F241.
*/
func TestDeletingAnInboundTakesItsRowsWithIt(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	inbound := durableTestInbound(nil, "orphan-source", 24901)
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	child := durableTestInbound(nil, "orphan-child", 24902)
	if err := db.Create(child).Error; err != nil {
		t.Fatalf("seed child: %v", err)
	}

	if err := db.Create(&model.ClientCredential{
		ClientId: 1, InboundId: inbound.Id, Key: "secret", Value: "s3cret",
	}).Error; err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	if err := db.Create(&model.InboundFallback{
		MasterId: inbound.Id, ChildId: child.Id, Name: "h2",
	}).Error; err != nil {
		t.Fatalf("seed fallback: %v", err)
	}

	if _, err := (&InboundService{}).DelInbound(inbound.Id); err != nil {
		t.Fatalf("DelInbound: %v", err)
	}

	var credentials, fallbacks int64
	if err := db.Model(&model.ClientCredential{}).Where("inbound_id = ?", inbound.Id).Count(&credentials).Error; err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if credentials != 0 {
		t.Fatalf("%d credential row(s) outlived their inbound; a recycled id adopts them", credentials)
	}
	if err := db.Model(&model.InboundFallback{}).
		Where("master_id = ? OR child_id = ?", inbound.Id, inbound.Id).
		Count(&fallbacks).Error; err != nil {
		t.Fatalf("count fallbacks: %v", err)
	}
	if fallbacks != 0 {
		t.Fatalf("%d fallback row(s) outlived their inbound", fallbacks)
	}
}

/*
And the backlog is cleared at startup.

Every database that has ever deleted an inbound already holds orphans, and the
fix at the delete site cannot reach them. The pruners run before
resyncPostgresSequences — before the sequence is rewound and the id becomes
available again.
*/
func TestStartupPrunesTheOrphanBacklog(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	// Rows whose inbound never existed, which is what a pre-fix delete left.
	const goneID = 987654
	if err := db.Create(&model.ClientCredential{
		ClientId: 1, InboundId: goneID, Key: "secret", Value: "s3cret",
	}).Error; err != nil {
		t.Fatalf("seed orphan credential: %v", err)
	}
	if err := db.Create(&model.InboundFallback{
		MasterId: goneID, ChildId: 0, Name: "dest-only", Dest: "8443",
	}).Error; err != nil {
		t.Fatalf("seed orphan fallback: %v", err)
	}

	// A dest-only fallback on a LIVE inbound: child_id 0 is the sentinel for
	// "no child", not a dangling reference, and must survive the pruner.
	live := durableTestInbound(nil, "orphan-survivor", 24903)
	if err := db.Create(live).Error; err != nil {
		t.Fatalf("seed live inbound: %v", err)
	}
	if err := db.Create(&model.InboundFallback{
		MasterId: live.Id, ChildId: 0, Name: "keep-me", Dest: "8443",
	}).Error; err != nil {
		t.Fatalf("seed live fallback: %v", err)
	}

	if err := database.PruneOrphansForTest(); err != nil {
		t.Fatalf("prune: %v", err)
	}

	var orphanCredentials, orphanFallbacks, liveFallbacks int64
	_ = db.Model(&model.ClientCredential{}).Where("inbound_id = ?", goneID).Count(&orphanCredentials)
	_ = db.Model(&model.InboundFallback{}).Where("master_id = ?", goneID).Count(&orphanFallbacks)
	_ = db.Model(&model.InboundFallback{}).Where("master_id = ?", live.Id).Count(&liveFallbacks)

	if orphanCredentials != 0 || orphanFallbacks != 0 {
		t.Fatalf("backlog survived: %d credential(s), %d fallback(s)", orphanCredentials, orphanFallbacks)
	}
	if liveFallbacks != 1 {
		t.Fatalf("a dest-only fallback on a live inbound was pruned: child_id 0 is the sentinel for no child, not a dangling reference")
	}
}
