package tgbot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mymmrac/telego"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
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

/*
The end-to-end half. sendClientIndividualLinks fetches the subscription page over
HTTP to render the per-client URIs, so a forged callback that got through would
leave a request for the victim's subId in the sub server's log. Nothing arriving
there is the assertion.
*/
func TestForgedCallbackFetchesNothingForAnotherClient(t *testing.T) {
	initTgbotDB(t)
	seedClient(t, 31003, "in-victim", "victim@example.com", "victimsub12345678", victimTgID)
	seedClient(t, 31004, "in-attacker", "attacker@example.com", "attackersub123456", attackerTgID)

	var served atomic.Int64
	var paths []string
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte("vless://11111111-2222-3333-4444-555555555555@host:443\n"))
	}))
	defer sub.Close()
	setSetting(t, "subURI", sub.URL+"/sub/")

	// The real client the fetch path uses; the bot start that normally builds it
	// has not run here, and a nil one would fail the served path for the wrong
	// reason.
	optimizedHTTPClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() { optimizedHTTPClient = nil })

	tg := &Tgbot{}
	forged := &telego.CallbackQuery{
		ID:      "cb-forged",
		Data:    "client_individual_links victim@example.com",
		From:    telego.User{ID: attackerTgID},
		Message: &telego.Message{Chat: telego.Chat{ID: 4242}},
	}
	tg.answerCallback(forged, false)

	if n := served.Load(); n != 0 {
		t.Fatalf("the sub server served %d request(s) %v for a client the caller does not own", n, paths)
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

	if n := served.Load(); n != 1 {
		t.Fatalf("the sub server served %d request(s) for the caller's own client, want 1", n)
	}
	if len(paths) != 1 || !strings.Contains(paths[0], "attackersub123456") {
		t.Fatalf("served paths = %v, want one carrying the caller's own subId", paths)
	}
}
