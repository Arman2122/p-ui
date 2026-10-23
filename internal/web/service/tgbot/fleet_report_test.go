package tgbot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func fleetReportClient(email string, chat int64) service.FleetStatsClient {
	reserved := int64(9000)
	return service.FleetStatsClient{TelegramID: chat, Attached: true, EnabledInbound: true, TelegramEligible: true, Traffic: core.ClientTraffic{
		Email: email, Enable: true, Up: 40, Down: 60, Total: 200,
		Accounting: &core.TrafficAccounting{Source: "fleet", State: "active", TermID: "term", MeasuredUploaded: 30, MeasuredDownloaded: 40, OpeningUploaded: 10, OpeningDownloaded: 20, ReservedBytes: &reserved},
	}}
}

func fleetReportSnapshot() service.FleetStatsSnapshot {
	return service.FleetStatsSnapshot{
		GeneratedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), Schedule: "@daily", TrafficThreshold: 101,
		ClientCount: 10, MeasuredBytes: 700, OpeningBytes: 300,
		Clients: []service.FleetStatsClient{fleetReportClient("alice<img>", 33)},
		Nodes: []service.FleetStatsNode{{
			ID: "node", Name: "edge & one", State: "disconnected", DesiredRevision: 8, AppliedRevision: 7, LastSeenUnixMilli: 1000,
			Runtimes: []service.FleetStatsRuntime{{ID: "xray", State: "stale", ObservedUnixMilli: 1000}},
		}},
	}
}

func TestFleetStatsReportKeepsMeasuredOpeningAndUnknownSeparate(t *testing.T) {
	planBotLocale(t)
	snapshot := fleetReportSnapshot()
	messages, err := (&Tgbot{}).fleetReportMessages(snapshot, "management<host>", []int64{11})
	if err != nil || len(messages) != 2 {
		t.Fatalf("fleet report=%+v %v", messages, err)
	}
	text := messages[0].text
	for _, want := range []string{"Management host: management&lt;host&gt;", "Current account totals (10 clients)", "Measured traffic: 700.00B", "Opening balances: 300.00B", "Measured: 70.00B · opening: 30.00B", "No recent contact", "Stale observation", "applied 7 / desired 8", "uncertain usage: Unknown", "expiry: Unlimited", "received: Unknown", "alice&lt;img&gt;", "edge &amp; one", "1970-01-01T00:00:01Z"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report omitted %q: %q", want, text)
		}
	}
	if strings.Contains(text, "Measured traffic: 9") || strings.Contains(text, "Xray Version") || strings.Contains(text, "Onlines") || strings.Contains(text, "alice<img>") {
		t.Fatalf("fleet report conflated authority, host execution or unescaped input: %q", text)
	}
	snapshot.Clients[0].Traffic.Total = 20000
	messages, err = (&Tgbot{}).fleetReportMessages(snapshot, "master", []int64{11})
	if err != nil || len(messages) != 1 || !strings.Contains(messages[0].text, "near quota or expiry: 0") {
		t.Fatalf("reservation caused an exhaustion notice: %+v %v", messages, err)
	}
}

func TestFleetStatsFakeDeliveryPreservesRecipientsAndBackupScope(t *testing.T) {
	planBotLocale(t)
	requests := captureBotRequests(t)
	tgBotMutex.Lock()
	previous := adminIds
	adminIds = []int64{11, 22}
	tgBotMutex.Unlock()
	t.Cleanup(func() { tgBotMutex.Lock(); adminIds = previous; tgBotMutex.Unlock() })
	snapshot := fleetReportSnapshot()
	snapshot.Backup = true
	admin := fleetReportClient("admin-client", 11)
	second := fleetReportClient("second-client", 33)
	disabled := fleetReportClient("disabled-client", 33)
	disabled.Traffic.Enable = false
	noEnabledInbound := fleetReportClient("disabled-inbound-only", 44)
	noEnabledInbound.EnabledInbound = false
	noEnabledInbound.TelegramEligible = false
	noNear := fleetReportClient("headroom", 55)
	noNear.Traffic.Total = 20000
	snapshot.Clients = append(snapshot.Clients, admin, second, disabled, noEnabledInbound, noNear)
	path := filepath.Join(t.TempDir(), "management.dump")
	if err := os.WriteFile(path, []byte("management-database-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	backups := 0
	backup := func(ctx context.Context) error {
		backups++
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		return SendBackupArchive(ctx, file, "management.dump")
	}
	if err := (&Tgbot{}).sendFleetSnapshot(t.Context(), snapshot, "management", backup, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	texts, documents := map[int64]string{}, map[int64]int{}
	for _, request := range requests() {
		switch request.Method {
		case "sendMessage":
			texts[request.ChatID] += request.Text
		case "sendDocument":
			if request.Document != "management-database-only" {
				t.Fatalf("backup included unexpected local runtime data: %+v", request)
			}
			documents[request.ChatID]++
		default:
			t.Fatalf("unexpected Telegram operation: %+v", request)
		}
	}
	if backups != 1 || documents[11] != 1 || documents[22] != 1 || len(documents) != 2 || len(texts) != 3 || texts[44] != "" || texts[55] != "" {
		t.Fatalf("recipient/backup policy changed: backups=%d docs=%v textChats=%v", backups, documents, texts)
	}
	if strings.Count(texts[33], "Client second-client\n") != 1 || !strings.Contains(texts[33], "Disabled clients: disabled-client") || strings.Contains(texts[33], "Management host:") || strings.Contains(texts[33], "admin-client") {
		t.Fatalf("shared client chat received duplicate or foreign data: %q", texts[33])
	}
	if !strings.Contains(texts[11], "excludes agent state and runtime credit journals") || strings.Count(texts[11], "Client admin-client\n") != 1 {
		t.Fatalf("administrator report/backup scope ambiguous: %q", texts[11])
	}
	snapshot.Backup = false
	if err := (&Tgbot{}).sendFleetSnapshot(t.Context(), snapshot, "management", backup, func() error { return nil }); err != nil || backups != 1 {
		t.Fatalf("disabled scheduled backup ran: count=%d error=%v", backups, err)
	}
}

func TestFleetStatsReturnsDeliveryAndBackupFailures(t *testing.T) {
	planBotLocale(t)
	requests := captureBotRequests(t, "simulated delivery rejection")
	tgBotMutex.Lock()
	previous := adminIds
	adminIds = []int64{11}
	tgBotMutex.Unlock()
	t.Cleanup(func() { tgBotMutex.Lock(); adminIds = previous; tgBotMutex.Unlock() })
	snapshot := fleetReportSnapshot()
	snapshot.Backup = true
	backupErr := errors.New("simulated backup failure")
	err := (&Tgbot{}).sendFleetSnapshot(t.Context(), snapshot, "management", func(context.Context) error { return backupErr }, func() error { return nil })
	if !errors.Is(err, backupErr) || !strings.Contains(err.Error(), "simulated delivery rejection") || len(requests()) != 2 {
		t.Fatalf("fleet report discarded delivery/backup errors: %v requests=%+v", err, requests())
	}
}

func TestFleetStatsStopsEmissionWhenLifecycleOrDatabaseChanges(t *testing.T) {
	for _, reason := range []error{context.Canceled, service.ErrFleetStatsDB} {
		t.Run(reason.Error(), func(t *testing.T) {
			planBotLocale(t)
			requests := captureBotRequests(t)
			tgBotMutex.Lock()
			previous := adminIds
			adminIds = []int64{11, 22}
			tgBotMutex.Unlock()
			t.Cleanup(func() { tgBotMutex.Lock(); adminIds = previous; tgBotMutex.Unlock() })
			backups := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			check := func() error {
				if len(requests()) > 0 {
					if errors.Is(reason, context.Canceled) {
						cancel()
						return ctx.Err()
					}
					return reason
				}
				return nil
			}
			snapshot := fleetReportSnapshot()
			snapshot.Backup = true
			err := (&Tgbot{}).sendFleetSnapshot(ctx, snapshot, "management", func(context.Context) error { backups++; return nil }, check)
			if !errors.Is(err, reason) || backups != 0 || len(requests()) != 1 || requests()[0].ChatID != 11 {
				t.Fatalf("stale lifecycle emitted later report/backup: error=%v backups=%d requests=%+v", err, backups, requests())
			}
		})
	}
}

func TestFleetStatsBackupRechecksDatabaseBeforeEachRecipient(t *testing.T) {
	requests := captureBotRequests(t)
	tgBotMutex.Lock()
	previous := adminIds
	adminIds = []int64{11, 22}
	tgBotMutex.Unlock()
	t.Cleanup(func() { tgBotMutex.Lock(); adminIds = previous; tgBotMutex.Unlock() })
	file, err := os.CreateTemp(t.TempDir(), "management-*.dump")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("management-database-only"); err != nil {
		t.Fatal(err)
	}
	checks := 0
	err = sendBackupArchive(t.Context(), file, "management.dump", func() error {
		checks++
		if checks > 1 {
			return service.ErrFleetStatsDB
		}
		return nil
	})
	if !errors.Is(err, service.ErrFleetStatsDB) || len(requests()) != 1 || requests()[0].ChatID != 11 || requests()[0].Method != "sendDocument" {
		t.Fatalf("stale backup sent to later recipient: %v %+v", err, requests())
	}
}
