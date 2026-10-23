package tgbot

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/util/common"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func (t *Tgbot) SendFleetReport(ctx context.Context, db *gorm.DB) error {
	if !t.IsRunning() {
		return nil
	}
	snapshot, err := service.ReadFleetStats(ctx, db, time.Now())
	if err != nil {
		return err
	}
	managementHost, err := os.Hostname()
	if err != nil {
		return err
	}
	if managementHost == "" {
		managementHost = "penhoon-management"
	}
	check := func() error { return service.ValidateFleetStatsDB(ctx, db) }
	return t.sendFleetSnapshot(ctx, snapshot, managementHost, func(ctx context.Context) error { return t.sendFleetDatabaseBackup(ctx, db, managementHost) }, check)
}

func (t *Tgbot) sendFleetDatabaseBackup(ctx context.Context, db *gorm.DB, managementHost string) error {
	check := func() error { return service.ValidateFleetStatsDB(ctx, db) }
	if err := check(); err != nil {
		return err
	}
	dump, err := t.serverService.GetDbContext(ctx)
	if err != nil {
		return err
	}
	defer dump.Close()
	if err := check(); err != nil {
		return err
	}
	return sendBackupArchive(ctx, dump.File, t.serverService.BackupFilename(managementHost), check)
}

type fleetReportMessage struct {
	chat int64
	text string
}

func (t *Tgbot) sendFleetSnapshot(ctx context.Context, snapshot service.FleetStatsSnapshot, managementHost string, backup func(context.Context) error, check func() error) error {
	if err := check(); err != nil {
		return err
	}
	tgBotMutex.Lock()
	sender, running := bot, isRunning
	admins := slices.Clone(adminIds)
	tgBotMutex.Unlock()
	if !running || sender == nil {
		return errors.New("Telegram bot is unavailable")
	}
	messages, err := t.fleetReportMessages(snapshot, managementHost, admins)
	if err != nil {
		return err
	}
	var failures []error
	for _, message := range messages {
		if err := sendFleetReportText(ctx, sender, message.chat, message.text, check); err != nil {
			failures = append(failures, err)
		}
	}
	if snapshot.Backup {
		if err := check(); err != nil {
			failures = append(failures, err)
		} else if backup == nil {
			failures = append(failures, errors.New("fleet database backup is unavailable"))
		} else if err := backup(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func sendFleetReportText(ctx context.Context, sender *telego.Bot, chat int64, text string, check func() error) error {
	for i, page := range pageMessage(text) {
		if i > 0 {
			select {
			case <-time.After(100 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := check(); err != nil {
			return err
		}
		deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
		var err error
		if len(page) > telegramPageLimit {
			_, err = sender.SendDocument(deadline, tu.Document(tu.ID(chat), tu.FileFromReader(strings.NewReader(page), "fleet-report.txt")))
		} else {
			_, err = sender.SendMessage(deadline, &telego.SendMessageParams{ChatID: tu.ID(chat), Text: page, ParseMode: "HTML"})
		}
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *Tgbot) fleetReportMessages(snapshot service.FleetStatsSnapshot, managementHost string, admins []int64) ([]fleetReportMessage, error) {
	var notices []service.FleetStatsClient
	chats := make(map[int64][]service.FleetStatsClient)
	for _, client := range snapshot.Clients {
		accounting := client.Traffic.Accounting
		if accounting == nil || accounting.Source != "fleet" {
			return nil, service.ErrFleetUsage
		}
		if client.EnabledInbound {
			if client.Traffic.Enable && service.FleetStatsNearLimit(client.Traffic, snapshot) {
				notices = append(notices, client)
			}
		}
		if client.TelegramID != 0 && client.Attached && client.TelegramEligible && !slices.Contains(admins, client.TelegramID) {
			chats[client.TelegramID] = append(chats[client.TelegramID], client)
		}
	}
	var text strings.Builder
	text.WriteString(t.I18nBot("tgbot.messages.fleetReport", "Host=="+managementHost, "Version=="+config.GetPanelVersion(), "Time=="+snapshot.GeneratedAt.UTC().Format(time.RFC3339), "Schedule=="+snapshot.Schedule))
	text.WriteString(t.I18nBot("tgbot.messages.fleetTotals", "Count=="+strconv.FormatInt(snapshot.ClientCount, 10), "Measured=="+common.FormatTraffic(snapshot.MeasuredBytes), "Opening=="+common.FormatTraffic(snapshot.OpeningBytes)))
	for _, node := range snapshot.Nodes {
		text.WriteString(t.I18nBot("tgbot.messages.fleetNode", "Name=="+node.Name, "State=="+t.fleetReportState(node.State),
			"Applied=="+strconv.FormatInt(node.AppliedRevision, 10), "Desired=="+strconv.FormatInt(node.DesiredRevision, 10),
			"Contact=="+t.fleetReportTime(node.LastSeenUnixMilli), "Received=="+t.fleetReportTime(node.ReceivedUnixMilli), "Observed=="+t.fleetReportTime(node.ObservedUnixMilli)))
		for _, runtime := range node.Runtimes {
			text.WriteString(t.I18nBot("tgbot.messages.fleetRuntime", "Name=="+runtime.ID, "State=="+t.fleetReportState(runtime.State), "Received=="+t.fleetReportTime(runtime.ReceivedUnixMilli), "Observed=="+t.fleetReportTime(runtime.ObservedUnixMilli)))
		}
	}
	text.WriteString(t.fleetClientNotices(notices, snapshot.DisabledClients))
	if snapshot.Backup {
		text.WriteString(t.I18nBot("tgbot.messages.fleetBackupScope"))
	}
	var result []fleetReportMessage
	for _, chat := range admins {
		result = append(result, fleetReportMessage{chat: chat, text: text.String()})
	}
	ids := make([]int64, 0, len(chats))
	for id := range chats {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, chat := range ids {
		var near []service.FleetStatsClient
		var disabledClients []string
		for _, client := range chats[chat] {
			if !client.Traffic.Enable {
				disabledClients = append(disabledClients, client.Traffic.Email)
			} else if service.FleetStatsNearLimit(client.Traffic, snapshot) {
				near = append(near, client)
			}
		}
		if len(near) > 0 {
			message := t.fleetClientNotices(near, int64(len(disabledClients)))
			if len(disabledClients) > 0 {
				message += t.I18nBot("tgbot.messages.fleetDisabledClients", "Clients=="+strings.Join(disabledClients, ", "))
			}
			result = append(result, fleetReportMessage{chat: chat, text: message})
		}
	}
	return result, nil
}

func (t *Tgbot) fleetClientNotices(clients []service.FleetStatsClient, disabled int64) string {
	var text strings.Builder
	text.WriteString(t.I18nBot("tgbot.messages.fleetNotices", "Count=="+strconv.Itoa(len(clients)), "Disabled=="+strconv.FormatInt(disabled, 10)))
	for _, client := range clients {
		traffic, accounting := client.Traffic, client.Traffic.Accounting
		quota := t.I18nBot("tgbot.messages.fleetUnlimited")
		if traffic.Total > 0 {
			quota = common.FormatTraffic(traffic.Total)
		}
		expiry := t.I18nBot("tgbot.messages.fleetUnlimited")
		if traffic.ExpiryTime > 0 {
			expiry = t.fleetReportTime(traffic.ExpiryTime)
		}
		text.WriteString(t.I18nBot("tgbot.messages.fleetClient", "Email=="+traffic.Email,
			"Measured=="+common.FormatTraffic(accounting.MeasuredUploaded+accounting.MeasuredDownloaded), "Opening=="+common.FormatTraffic(accounting.OpeningUploaded+accounting.OpeningDownloaded),
			"Reserved=="+t.fleetReportAmount(accounting.ReservedBytes), "Uncertain=="+t.fleetReportAmount(accounting.UncertainBytes),
			"Quota=="+quota, "Expiry=="+expiry, "Received=="+t.fleetReportTime(accounting.ReportedUnixMilli), "Observed=="+t.fleetReportTime(accounting.ObservedUnixMilli)))
	}
	return text.String()
}

func (t *Tgbot) fleetReportTime(timestamp int64) string {
	if timestamp <= 0 {
		return t.I18nBot("tgbot.messages.fleetUnknown")
	}
	return time.UnixMilli(timestamp).UTC().Format(time.RFC3339)
}

func (t *Tgbot) fleetReportAmount(amount *int64) string {
	if amount == nil {
		return t.I18nBot("tgbot.messages.fleetUnknown")
	}
	return common.FormatTraffic(*amount)
}

func (t *Tgbot) fleetReportState(state string) string {
	switch state {
	case "ready":
		return t.I18nBot("tgbot.messages.fleetReady")
	case "degraded":
		return t.I18nBot("tgbot.messages.fleetDegraded")
	case "stale":
		return t.I18nBot("tgbot.messages.fleetStale")
	case "disabled":
		return t.I18nBot("tgbot.messages.fleetDisabled")
	case "disconnected":
		return t.I18nBot("tgbot.messages.fleetDisconnected")
	default:
		return t.I18nBot("tgbot.messages.fleetUnknown")
	}
}
