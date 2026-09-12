package tgbot

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"

	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/web/locale"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func planBotLocale(t *testing.T) {
	t.Helper()
	bundle := i18n.NewBundle(language.English)
	data, err := os.ReadFile(filepath.Join("..", "..", "translation", "en-US.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Tgbot struct{ Messages map[string]string }
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for key, value := range document.Tgbot.Messages {
		if err := bundle.AddMessages(language.English, &i18n.Message{ID: "tgbot.messages." + key, Other: value}); err != nil {
			t.Fatal(err)
		}
	}
	previous := locale.LocalizerBot
	locale.LocalizerBot = i18n.NewLocalizer(bundle, "en-US")
	t.Cleanup(func() { locale.LocalizerBot = previous })
}

func TestPlanDepletedTelegramFormatting(t *testing.T) {
	planBotLocale(t)
	e := eventbus.Event{
		Type: eventbus.EventClientPlanDepleted, Source: "plan-client", Timestamp: time.Now(),
		Data: service.PlanDepletionNotice{Email: "alice<img>@example.com", PlanName: "Family & friends", UsedBytes: 150, QuotaBytes: 100},
	}
	message := (&Tgbot{}).formatEventMessage(e)
	want := "Plan quota reached\nalice&lt;img&gt;@example.com reached the quota for plan Family &amp; friends: 150 of 100 bytes."
	if !strings.Contains(message, want) || strings.Contains(message, "<img>") || strings.Contains(message, "Device limit") {
		t.Fatalf("message=%q, want escaped plan text %q", message, want)
	}
	e.Data = nil
	if message := (&Tgbot{}).formatEventMessage(e); message != "" {
		t.Fatalf("nil payload sent %q", message)
	}
}

func TestPlanDepletedTelegramSelectionAndFakeDelivery(t *testing.T) {
	initTgbotDB(t)
	planBotLocale(t)
	var mu sync.Mutex
	var messages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") || request.ChatID != 11 {
			t.Errorf("unexpected delivery %s to %d", r.URL.Path, request.ChatID)
		}
		mu.Lock()
		messages = append(messages, request.Text)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":11,"type":"private"}}}`)
	}))
	defer server.Close()
	sender, err := telego.NewBot("1234567890:aaaabbbbaaaabbbbaaaabbbbaaaabbbbccc", telego.WithAPIServer(server.URL), telego.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	tgBotMutex.Lock()
	previousBot, previousRunning, previousAdmins := bot, isRunning, adminIds
	bot, isRunning, adminIds = sender, true, []int64{11}
	tgBotMutex.Unlock()
	previousLimiter := tgEventLimiter
	tgEventLimiter = eventbus.NewRateLimiter(time.Minute)
	t.Cleanup(func() {
		tgBotMutex.Lock()
		bot, isRunning, adminIds = previousBot, previousRunning, previousAdmins
		tgBotMutex.Unlock()
		tgEventLimiter = previousLimiter
	})
	s := service.SettingService{}
	if err := s.SetTgEnabledEvents("client.device_limit"); err != nil {
		t.Fatal(err)
	}
	e := eventbus.Event{
		Type: eventbus.EventClientPlanDepleted, Source: "selected", Timestamp: time.Now(),
		Data: service.PlanDepletionNotice{Email: "selected", PlanName: "Family", UsedBytes: 150, QuotaBytes: 100},
	}
	consumer := &Tgbot{}
	consumer.HandleEvent(e)
	mu.Lock()
	deliveries := len(messages)
	mu.Unlock()
	if deliveries != 0 {
		t.Fatalf("unselected event delivered %d messages", deliveries)
	}
	if err := s.SetTgEnabledEvents(" client.plan_depleted ,login.attempt"); err != nil {
		t.Fatal(err)
	}
	malformed := e
	malformed.Data = nil
	consumer.HandleEvent(malformed)
	consumer.HandleEvent(e)
	consumer.HandleEvent(e)
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 || !strings.Contains(messages[0], "selected reached the quota for plan Family: 150 of 100 bytes.") {
		t.Fatalf("fake Telegram deliveries=%v, want exactly one plan notice", messages)
	}
}
