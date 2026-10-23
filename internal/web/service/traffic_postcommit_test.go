package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestTrafficLifecycleWaitsForCommit(t *testing.T) {
	for _, action := range []string{"renew", "disable client", "disable inbound", "reset"} {
		for _, failCommit := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/fail=%v", action, failCommit), func(t *testing.T) {
				initTestDB(t)
				db := database.GetDB()
				previous := runtime.GetManager()
				manager := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
				fake := &fakeNodeRuntime{}
				manager.SetLocalRuntimeOverride(fake)
				runtime.SetManager(manager)
				t.Cleanup(func() { runtime.SetManager(previous) })
				client := model.Client{Email: "postcommit-client", ID: "11111111-1111-1111-1111-111111111111", Enable: true}
				traffic := xray.ClientTraffic{Email: client.Email, Enable: true}
				if action == "renew" {
					client.Reset, traffic.Reset = 30, 30
					client.ExpiryTime = time.Now().Add(-time.Hour).UnixMilli()
					traffic.ExpiryTime = client.ExpiryTime
					client.Enable = false
				}
				if action == "disable client" {
					client.TotalGB, traffic.Total, traffic.Up = 100, 100, 100
				}
				inbound := mkInbound(t, 30140, model.VLESS, clientsSettings(t, []model.Client{client}))
				if err := (&ClientService{}).SyncInbound(db, inbound.Id, []model.Client{client}); err != nil {
					t.Fatal(err)
				}
				traffic.InboundId = inbound.Id
				if err := db.Create(&traffic).Error; err != nil {
					t.Fatal(err)
				}
				if action == "renew" || action == "reset" {
					if err := db.Model(&traffic).Update("enable", false).Error; err != nil {
						t.Fatal(err)
					}
				}
				if action == "disable inbound" {
					if err := db.Model(inbound).Updates(map[string]any{"total": 100, "up": 100}).Error; err != nil {
						t.Fatal(err)
					}
				}
				if failCommit {
					kind, table := "update", "client_traffics"
					if action == "renew" {
						kind = "create"
					}
					if action == "disable inbound" {
						table = "inbounds"
					}
					installDeferredCommitFailure(t, db, kind, "test:maintenance_commit", table, "maintenance_parent", "maintenance_child")
				}
				svc := &InboundService{}
				var err error
				if action == "reset" {
					_, err = svc.ResetClientTraffic(inbound.Id, client.Email)
				} else {
					_, _, err = svc.AddTraffic(nil, nil)
				}
				var pgErr *pgconn.PgError
				if failCommit && (!errors.As(err, &pgErr) || pgErr.Code != "23503") {
					t.Fatalf("commit error = %v, want PostgreSQL foreign-key failure", err)
				}
				if !failCommit && err != nil {
					t.Fatal(err)
				}
				calls := fake.addUser.Load() + fake.removeUser.Load() + fake.delInbound.Load()
				want := int32(1)
				if failCommit {
					want = 0
				}
				if calls != want {
					t.Fatalf("runtime effects = %d (add=%d remove=%d drop=%d), want %d", calls, fake.addUser.Load(), fake.removeUser.Load(), fake.delInbound.Load(), want)
				}
			})
		}
	}
}

func TestBulkNodeMutationWaitsForCommit(t *testing.T) {
	for _, action := range []string{"adjust", "delete"} {
		t.Run(action, func(t *testing.T) {
			initTestDB(t)
			db := database.GetDB()
			nodeID, fake := setupNodeRuntime(t)
			client := model.Client{Email: "bulk-postcommit", ID: "11111111-1111-1111-1111-111111111111", Enable: true, ExpiryTime: time.Now().Add(time.Hour).UnixMilli()}
			ib := nodeInbound(t, nodeID, 30141, []model.Client{client})
			if err := db.Create(&xray.ClientTraffic{InboundId: ib.Id, Email: client.Email, Up: 100, Enable: true}).Error; err != nil {
				t.Fatal(err)
			}
			installDeferredCommitFailure(t, db, "update", "test:bulk_commit", "inbounds", "bulk_commit_parent", "bulk_commit_child")
			svc, inbounds := &ClientService{}, &InboundService{}
			if action == "adjust" {
				_, _, _ = svc.BulkAdjust(inbounds, []string{client.Email}, 1, 0, "", "")
			} else {
				_, _, _ = svc.BulkDelete(inbounds, []string{client.Email}, false)
			}
			if calls := fake.updateUser.Load() + fake.deleteClient.Load(); calls != 0 {
				t.Fatalf("node changed before a failed commit: update=%d delete=%d", fake.updateUser.Load(), fake.deleteClient.Load())
			}
			stored, err := inbounds.GetInbound(ib.Id)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Settings != ib.Settings {
				t.Fatalf("inbound settings committed despite rejected transaction: %s", stored.Settings)
			}
			var traffic xray.ClientTraffic
			if err := db.Where("email = ?", client.Email).First(&traffic).Error; err != nil || traffic.Up != 100 {
				t.Fatalf("failed mutation lost traffic: %+v, %v", traffic, err)
			}
		})
	}
}
