package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/authority"
)

func TestFleetAuthorityAcknowledgmentDoesNotLockMutableClient(t *testing.T) {
	f := newAuthorityFixture(t)
	batch := f.queue(t, true)
	pool, err := f.f.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(4)
	held := f.f.db.Begin()
	if held.Error != nil {
		t.Fatal(held.Error)
	}
	defer held.Rollback()
	var client model.FleetClient
	if err := held.Clauses(clause.Locking{Strength: "UPDATE"}).First(&client, "id = ?", f.materialized.Client.ID).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err = f.f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '250ms'").Error; err != nil {
			return err
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		receipt, err := store.AcknowledgeAuthorityUpdates(ctx, f.f.nodes[0], batch)
		if err != nil {
			return err
		}
		digest, err := authority.AuthorityDigest(batch)
		if err != nil {
			return err
		}
		if !bytes.Equal(receipt.GetPayloadSha256(), digest) {
			t.Error("acknowledgment returned a different authority batch digest")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("authority acknowledgment waited for a mutable client lock: %v", err)
	}
	var share model.FleetRateShare
	if err := f.f.db.First(&share, "id = ?", f.credit.Grant.ID).Error; err != nil || share.AppliedRevision != share.Revision || share.Outstanding != share.Desired {
		t.Fatalf("acknowledgment did not settle exact reserved rate: %+v %v", share, err)
	}
}
