package email

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"

	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/web/locale"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func planEmailLocale(t *testing.T) {
	t.Helper()
	bundle := goi18n.NewBundle(language.English)
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
		if err := bundle.AddMessages(language.English, &goi18n.Message{ID: "tgbot.messages." + key, Other: value}); err != nil {
			t.Fatal(err)
		}
	}
	previous := locale.LocalizerBot
	locale.LocalizerBot = goi18n.NewLocalizer(bundle, "en-US")
	t.Cleanup(func() { locale.LocalizerBot = previous })
}

func TestPlanDepletedEmailFormatting(t *testing.T) {
	planEmailLocale(t)
	e := eventbus.Event{
		Type: eventbus.EventClientPlanDepleted, Source: "plan-client", Timestamp: time.Now(),
		Data: service.PlanDepletionNotice{Email: "alice<img>@example.com", PlanName: "Family & friends", UsedBytes: 150, QuotaBytes: 100},
	}
	subject, body := (&Subscriber{}).formatMessage(e)
	if !strings.HasSuffix(subject, " Plan quota reached") {
		t.Fatalf("subject=%q", subject)
	}
	want := "alice&lt;img&gt;@example.com reached the quota for plan Family &amp; friends: 150 of 100 bytes."
	if !strings.Contains(body, want) || strings.Contains(body, "<img>") || strings.Contains(body, "Device limit") {
		t.Fatalf("body=%q, want escaped plan message %q", body, want)
	}
	e.Data = &eventbus.ClientDeviceLimitData{Email: "wrong-payload"}
	if subject, body := (&Subscriber{}).formatMessage(e); subject != "" || body != "" {
		t.Fatalf("wrong event payload produced %q / %q", subject, body)
	}
}

func TestPlanDepletedEmailSelectionAndFakeDelivery(t *testing.T) {
	initTestDB(t)
	planEmailLocale(t)
	addr, recorded := startFakeSMTPServer(t)
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	s := service.SettingService{}
	for _, err := range []error{
		s.SetSmtpHost(host), s.SetSmtpPort(port), s.SetSmtpFrom("panel@example.com"), s.SetSmtpTo("admin@example.com"),
		s.SetSmtpEncryptionType("none"), s.SetSmtpEnable(true), s.SetSmtpEnabledEvents("client.device_limit"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	subscriber := NewSubscriber(s, NewEmailService(s))
	e := eventbus.Event{
		Type: eventbus.EventClientPlanDepleted, Source: "selected", Timestamp: time.Now(),
		Data: service.PlanDepletionNotice{Email: "selected", PlanName: "Family", UsedBytes: 150, QuotaBytes: 100},
	}
	subscriber.HandleEvent(e)
	if got := recorded(); len(got) != 0 {
		t.Fatalf("unselected event sent SMTP data: %v", got)
	}
	if err := s.SetSmtpEnabledEvents(" client.plan_depleted ,login.attempt"); err != nil {
		t.Fatal(err)
	}
	malformed := e
	malformed.Data = nil
	subscriber.HandleEvent(malformed)
	subscriber.HandleEvent(e)
	subscriber.HandleEvent(e)
	var deliveries int
	for _, line := range recorded() {
		if strings.HasPrefix(line, "MAIL FROM:") {
			deliveries++
		}
	}
	if deliveries != 1 {
		t.Fatalf("fake SMTP deliveries=%d, want 1", deliveries)
	}
	if body := strings.Join(recorded(), "\n"); !strings.Contains(body, "selected reached the quota for plan Family: 150 of 100 bytes.") {
		t.Fatalf("fake SMTP did not receive plan text: %s", body)
	}
}
