package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

func TestManualClientRemovalHonorsXrayRestartSetting(t *testing.T) {
	operations := []struct {
		name string
		run  func(*ClientService, *InboundService, *model.Inbound, model.Client) (bool, error)
	}{
		{"disable", func(s *ClientService, is *InboundService, ib *model.Inbound, c model.Client) (bool, error) {
			c.Enable = false
			return s.UpdateByEmail(is, c.Email, c)
		}},
		{"delete", func(s *ClientService, is *InboundService, ib *model.Inbound, c model.Client) (bool, error) {
			return s.DeleteByEmail(is, c.Email, false)
		}},
		{"detach", func(s *ClientService, is *InboundService, ib *model.Inbound, c model.Client) (bool, error) {
			return s.DetachByEmail(is, ib.Id, c.Email)
		}},
		{"bulk disable", func(s *ClientService, is *InboundService, ib *model.Inbound, c model.Client) (bool, error) {
			_, nr, err := s.BulkSetEnable(is, []string{c.Email}, false)
			return nr, err
		}},
		{"bulk delete", func(s *ClientService, is *InboundService, ib *model.Inbound, c model.Client) (bool, error) {
			_, nr, err := s.BulkDelete(is, []string{c.Email}, false)
			return nr, err
		}},
		{"edit", func(s *ClientService, is *InboundService, ib *model.Inbound, c model.Client) (bool, error) {
			c.Comment = "still served"
			return s.UpdateByEmail(is, c.Email, c)
		}},
	}
	for _, on := range []bool{false, true} {
		for _, kind := range []model.Protocol{model.VLESS, model.MTProto} {
			for _, op := range operations {
				name := string(kind) + "/" + op.name
				if on {
					name += "/on"
				} else {
					name += "/off"
				}
				t.Run(name, func(t *testing.T) {
					setupBulkDB(t)
					if err := (&SettingService{}).SetRestartXrayOnClientDisable(on); err != nil {
						t.Fatal(err)
					}
					manager := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
					fake := &fakeNodeRuntime{}
					manager.SetLocalRuntimeOverride(fake)
					runtime.SetManager(manager)
					t.Cleanup(func() { runtime.SetManager(nil) })
					client := model.Client{Email: "manual-revoke", ID: "11111111-1111-1111-1111-111111111111", Secret: mtprotoTestSecretA, Enable: true, SubID: "manual-sub"}
					inbound := mkInbound(t, 47441, kind, clientsSettings(t, []model.Client{client}))
					if err := (&ClientService{}).SyncInbound(nil, inbound.Id, []model.Client{client}); err != nil {
						t.Fatal(err)
					}
					seedClientTraffic(t, inbound.Id, client.Email, true)
					nr, err := op.run(&ClientService{}, &InboundService{}, inbound, client)
					if err != nil {
						t.Fatal(err)
					}
					want := on && kind == model.VLESS && op.name != "edit"
					if nr != want {
						t.Fatalf("needRestart=%v, want %v", nr, want)
					}
					if _, pending := pendingXraySessionDrop(); pending != want {
						t.Fatalf("pending session revocation=%v, want %v", pending, want)
					}
					if op.name == "edit" || op.name == "disable" {
						if fake.updateUser.Load() != 1 {
							t.Fatalf("Runtime UpdateUser calls=%d", fake.updateUser.Load())
						}
					} else if fake.removeUser.Load() != 1 {
						t.Fatalf("Runtime RemoveUser calls=%d", fake.removeUser.Load())
					}
					var remaining model.Inbound
					if err := database.GetDB().First(&remaining, inbound.Id).Error; err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestRemoteClientRemovalDoesNotRestartMaster(t *testing.T) {
	for _, operation := range []string{"disable", "delete"} {
		t.Run(operation, func(t *testing.T) {
			setupBulkDB(t)
			if err := (&SettingService{}).SetRestartXrayOnClientDisable(true); err != nil {
				t.Fatal(err)
			}
			nodeID, fake := setupNodeRuntime(t)
			client := model.Client{Email: "remote-revoke", ID: "11111111-1111-1111-1111-111111111111", Enable: true, SubID: "remote-sub"}
			inbound := nodeInbound(t, nodeID, 47442, []model.Client{client})
			seedClientTraffic(t, inbound.Id, client.Email, true)
			var nr bool
			var err error
			if operation == "disable" {
				client.Enable = false
				nr, err = (&ClientService{}).UpdateByEmail(&InboundService{}, client.Email, client)
			} else {
				nr, err = (&ClientService{}).DeleteByEmail(&InboundService{}, client.Email, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			if nr {
				t.Fatal("remote revocation scheduled master Xray restart")
			}
			if operation == "disable" && fake.updateUser.Load() != 1 {
				t.Fatalf("remote update calls=%d", fake.updateUser.Load())
			}
			if operation == "delete" && fake.deleteClient.Load() != 1 {
				t.Fatalf("remote delete calls=%d", fake.deleteClient.Load())
			}
		})
	}
}

func TestFullInboundClientRevocationQueuesSessionDrop(t *testing.T) {
	for _, operation := range []string{"remove", "disable", "edit"} {
		t.Run(operation, func(t *testing.T) {
			setupConflictDB(t)
			if err := (&SettingService{}).SetRestartXrayOnClientDisable(true); err != nil {
				t.Fatal(err)
			}
			manager := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
			fake := &fakeNodeRuntime{}
			manager.SetLocalRuntimeOverride(fake)
			runtime.SetManager(manager)
			t.Cleanup(func() { runtime.SetManager(nil) })
			const email = "full-inbound-revoke"
			seedInboundConflict(t, "full-revoke", "", 47443, model.VLESS, "", `{"decryption":"none","clients":[{"email":"full-inbound-revoke","id":"11111111-1111-1111-1111-111111111111","enable":true}]}`)
			old := loadInboundByTag(t, "full-revoke")
			seedClientTraffic(t, old.Id, email, true)
			clients, err := (&InboundService{}).GetClients(old)
			if err != nil {
				t.Fatal(err)
			}
			if err := (&ClientService{}).SyncInbound(nil, old.Id, clients); err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "remove":
				clients = nil
			case "disable":
				clients[0].Enable = false
			case "edit":
				clients[0].Comment = "still served"
			}
			settings := map[string]any{"decryption": "none", "clients": clients}
			if clients == nil {
				settings["clients"] = []model.Client{}
			}
			raw, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			changed := *old
			changed.Settings = string(raw)
			_, nr, _, err := (&InboundService{}).UpdateInbound(&changed)
			if err != nil {
				t.Fatal(err)
			}
			want := operation != "edit"
			if nr != want {
				t.Fatalf("needRestart=%v, want %v", nr, want)
			}
			if _, pending := pendingXraySessionDrop(); pending != want {
				t.Fatalf("pending session revocation=%v, want %v", pending, want)
			}
			if fake.updateInbound.Load() != 1 {
				t.Fatalf("Runtime UpdateInbound calls=%d", fake.updateInbound.Load())
			}
		})
	}
}
