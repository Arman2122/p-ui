package tgbot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mymmrac/telego"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/sub"
	"github.com/Arman2122/p-ui/internal/web/service"
)

const (
	victimTgID   int64 = 111111
	attackerTgID int64 = 222222
)

// seedClient writes one client the way an inbound carries it — an entry in
// settings.clients[] plus its client_traffics row — because GetClientTrafficTgBot
// reads the first and GetClientByEmail keys on the second.
func seedClient(t *testing.T, port int, tag, email, subID string, tgID int64) {
	t.Helper()
	db := database.GetDB()

	settings, err := json.Marshal(map[string]any{
		"clients": []any{map[string]any{
			"id":    "11111111-2222-3333-4444-555555555555",
			"email": email,
			"subId": subID,
			"tgId":  tgID,
			"flow":  "",
		}},
		"decryption": "none",
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	inbound := &model.Inbound{
		UserId: 1, Enable: true, Tag: tag, Port: port, Protocol: model.VLESS,
		Listen: "0.0.0.0", Settings: string(settings),
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound %s: %v", tag, err)
	}
	if err := db.Create(&core.ClientTraffic{
		InboundId: inbound.Id, Enable: true, Email: email,
	}).Error; err != nil {
		t.Fatalf("create client_traffics for %s: %v", email, err)
	}
}

func setSetting(t *testing.T, key, value string) {
	t.Helper()
	if err := database.GetDB().Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}

/*
The callback payload is attacker-chosen text. Any Telegram user the bot has ever
answered can send `client_individual_links <someone else's email>`, and the
prefixed forms are on the non-admin whitelist because that is what the bot's own
buttons carry — encodeQuery only hashes past 64 characters, so an ordinary email
rides as plaintext. Ownership therefore has to be resolved before serving, not
inferred from the payload having the right shape.
*/
func TestOwnsEmailIsScopedToTheCallersTelegramId(t *testing.T) {
	initTgbotDB(t)
	seedClient(t, 31001, "in-victim", "victim@example.com", "victimsub12345678", victimTgID)
	seedClient(t, 31002, "in-attacker", "attacker@example.com", "attackersub123456", attackerTgID)

	tg := &Tgbot{}
	for _, tc := range []struct {
		name  string
		tgID  int64
		email string
		want  bool
	}{
		{name: "own client", tgID: victimTgID, email: "victim@example.com", want: true},
		{name: "somebody else's client", tgID: attackerTgID, email: "victim@example.com"},
		{name: "an email nobody has", tgID: attackerTgID, email: "nobody@example.com"},
		{name: "a Telegram id with no clients", tgID: 999999, email: "victim@example.com"},
		{name: "the empty email", tgID: victimTgID, email: ""},
		// Exact bytes, both here and in the lookup this guards: neither folds case.
		{name: "the same email in a different case", tgID: victimTgID, email: "VICTIM@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tg.ownsEmail(tc.tgID, tc.email); got != tc.want {
				t.Fatalf("ownsEmail(%d, %q) = %v, want %v", tc.tgID, tc.email, got, tc.want)
			}
		})
	}
}

// Ownership is checked before rendering local subscription links or sending a Telegram message.
func TestForgedCallbackFetchesNothingForAnotherClient(t *testing.T) {
	initTgbotDB(t)
	seedClient(t, 31003, "in-victim", "victim@example.com", "victimsub12345678", victimTgID)
	seedClient(t, 31004, "in-attacker", "attacker@example.com", "attackersub123456", attackerTgID)

	requests := captureBotRequests(t)
	service.RegisterSubLinkProvider(sub.NewLinkProvider())
	t.Cleanup(func() { service.RegisterSubLinkProvider(nil) })
	for _, entry := range []struct {
		email, subID, tag string
		tgID              int64
	}{
		{"victim@example.com", "victimsub12345678", "in-victim", victimTgID},
		{"attacker@example.com", "attackersub123456", "in-attacker", attackerTgID},
	} {
		var inbound model.Inbound
		if err := database.GetDB().Where("tag = ?", entry.tag).First(&inbound).Error; err != nil {
			t.Fatal(err)
		}
		client := model.ClientRecord{Email: entry.email, SubID: entry.subID, UUID: "11111111-2222-4333-8444-555555555555", Enable: true, TgID: entry.tgID}
		if err := database.GetDB().Create(&client).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.GetDB().Create(&model.ClientInbound{ClientId: client.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var served atomic.Int64
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		_, _ = w.Write([]byte("vless://11111111-2222-3333-4444-555555555555@host:443\n"))
	}))
	defer sub.Close()
	setSetting(t, "subURI", sub.URL+"/sub/")

	tg := &Tgbot{}
	forged := &telego.CallbackQuery{
		ID:      "cb-forged",
		Data:    "client_individual_links victim@example.com",
		From:    telego.User{ID: attackerTgID},
		Message: &telego.Message{Chat: telego.Chat{ID: 4242}},
	}
	tg.answerCallback(forged, false)

	if n := served.Load(); n != 0 {
		t.Fatalf("the sub server served %d request(s) for a client the caller does not own", n)
	}

	// The same call for the caller's OWN client must still be served, or the
	// guard has closed the feature rather than the hole.
	own := &telego.CallbackQuery{
		ID:      "cb-own",
		Data:    "client_individual_links attacker@example.com",
		From:    telego.User{ID: attackerTgID},
		Message: &telego.Message{Chat: telego.Chat{ID: 4242}},
	}
	tg.answerCallback(own, false)

	if n := served.Load(); n != 0 {
		t.Fatalf("local link rendering fetched the public URL %d times", n)
	}
	var links []string
	for _, request := range requests() {
		if request.Method == "sendMessage" {
			links = append(links, request.Text)
		}
	}
	if len(links) != 1 || !strings.Contains(links[0], ":31004") || strings.Contains(links[0], ":31003") {
		t.Fatalf("owned link delivery = %q, want only caller's inbound", links)
	}
}
