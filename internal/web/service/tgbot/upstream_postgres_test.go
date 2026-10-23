package tgbot

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/locale"
)

func upstreamBotLocale(t *testing.T) {
	t.Helper()
	bundle := i18n.NewBundle(language.English)
	if err := bundle.AddMessages(language.English,
		&i18n.Message{ID: "tgbot.messages.email", Other: "Email: {{ .Email }}\r\n"},
		&i18n.Message{ID: "tgbot.days", Other: "Days"},
		&i18n.Message{ID: "tgbot.messages.expireIn", Other: "Expire In: {{ .Time }}\r\n"},
		&i18n.Message{ID: "tgbot.messages.expire", Other: "Expire Date: {{ .Time }}\r\n"},
		&i18n.Message{ID: "tgbot.unlimited", Other: "Unlimited"},
		&i18n.Message{ID: "tgbot.messages.no", Other: "No"},
		&i18n.Message{ID: "tgbot.messages.yes", Other: "Yes"},
		&i18n.Message{ID: "tgbot.offline", Other: "Offline"},
		&i18n.Message{ID: "tgbot.messages.upload", Other: "Up: {{ .Upload }}\r\n"},
		&i18n.Message{ID: "tgbot.messages.download", Other: "Down: {{ .Download }}\r\n"},
		&i18n.Message{ID: "tgbot.messages.total", Other: "Used: {{ .UpDown }}\r\n"},
	); err != nil {
		t.Fatal(err)
	}
	previous := locale.LocalizerBot
	locale.LocalizerBot = i18n.NewLocalizer(bundle, "en")
	t.Cleanup(func() { locale.LocalizerBot = previous })
}

func TestInboundKeyboardUsesItsLoadedSnapshot(t *testing.T) {
	initTgbotDB(t)
	requests := captureBotRequests(t)
	inbound := &model.Inbound{
		UserId: 1, Enable: true, Port: 31445, Protocol: model.VLESS, Tag: "tg-snapshot", Remark: "snapshot",
		Settings: `{"clients":[{"email":"snapshot-client","id":"11111111-2222-4333-8444-555555555555"}]}`,
	}
	db := database.GetDB()
	if err := db.Create(inbound).Error; err != nil {
		t.Fatal(err)
	}
	reads := 0
	const callback = "test:remove-inbound-after-read"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "inbounds" {
			return
		}
		reads++
		if reads == 1 {
			if err := db.Exec("DELETE FROM inbounds WHERE id = ?", inbound.Id).Error; err != nil {
				t.Errorf("delete inbound: %v", err)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Errorf("stale inbound callback panicked: %v", recovered)
		}
	}()
	(&Tgbot{}).answerCallback(&telego.CallbackQuery{ID: "snapshot", From: telego.User{ID: 11}, Data: fmt.Sprintf("get_clients_for_sub %d", inbound.Id), Message: &telego.Message{Chat: telego.Chat{ID: -991}}}, true)
	if reads != 1 {
		t.Fatalf("inbound queries = %d, want 1", reads)
	}
	got := requests()
	if len(got) != 1 || got[0].Method != "sendMessage" {
		t.Fatalf("snapshot delivery = %+v, want one client keyboard", got)
	}
}

func TestDisabledDelayedClientShowsDuration(t *testing.T) {
	initTgbotDB(t)
	upstreamBotLocale(t)
	seedClient(t, 31449, "tg-delayed", "delayed", "delayed-sub", 11)
	got := (&Tgbot{}).clientInfoMsg(&core.ClientTraffic{Email: "delayed", Enable: false, ExpiryTime: -30 * 86400000}, false, false, false, true, false, false)
	if !strings.Contains(got, "Expire In: 30 Days") || strings.Contains(got, "1969") {
		t.Fatalf("delayed client expiry = %q, want 30 days until first use", got)
	}
}

func TestAddClientPickerRejectsNoEligibleInbounds(t *testing.T) {
	initTgbotDB(t)
	if err := database.GetDB().Create(&model.Inbound{UserId: 1, Enable: true, Port: 31446, Protocol: model.HTTP, Tag: "tg-no-clients", Settings: `{}`}).Error; err != nil {
		t.Fatal(err)
	}
	tg := &Tgbot{}
	keyboard, err := tg.getInboundsAddClient()
	if keyboard != nil || err == nil || err.Error() != tg.I18nBot("tgbot.answers.getInboundsFailed") {
		t.Fatalf("picker = %+v, %v; want getInboundsFailed", keyboard, err)
	}
}

func TestSortedTrafficReportBatchesClientMessages(t *testing.T) {
	initTgbotDB(t)
	upstreamBotLocale(t)
	requests := captureBotRequests(t)
	seedClient(t, 31447, "tg-alpha", "alpha", "alpha-sub", 11)
	seedClient(t, 31448, "tg-beta", "beta", "beta-sub", 11)
	(&Tgbot{}).answerCallback(&telego.CallbackQuery{ID: "report", From: telego.User{ID: 11}, Data: "get_sorted_traffic_usage_report", Message: &telego.Message{Chat: telego.Chat{ID: -991}}}, true)
	var messages []string
	for _, request := range requests() {
		if request.Method == "sendMessage" {
			messages = append(messages, request.Text)
		}
	}
	if len(messages) != 1 || !strings.Contains(messages[0], "Email: alpha") || !strings.Contains(messages[0], "Email: beta") {
		t.Fatalf("usage report = %q, want one message containing both clients", messages)
	}
}
