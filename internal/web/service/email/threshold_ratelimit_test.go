package email

import (
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/web/service"
)

/*
A quiet minute must not spend the alert's allowance.

HandleEvent took the rate limiter's token before formatMessage decided the
reading was below the operator's threshold and returned nothing. The CPU and
memory watchdogs publish on EVERY run whatever the reading is, at the same
one-minute cadence as the limiter's cooldown — so a normal reading spent the
token, and the spike that arrived next was dropped for a mail that was never
sent.

Driven at the seam rather than through SMTP: what is being measured is whether
the token was consumed, and the limiter is where that shows. F365.
*/
func TestABelowThresholdReadingDoesNotSpendTheAlertsToken(t *testing.T) {
	initTestDB(t)

	settingService := service.SettingService{}
	for name, err := range map[string]error{
		"enable":     settingService.SetSmtpEnable(true),
		"events":     settingService.SetSmtpEnabledEvents(string(eventbus.EventCPUHigh)),
		"cpu":        settingService.SetSmtpCpu(80),
		"host":       settingService.SetSmtpHost("127.0.0.1"),
		"port":       settingService.SetSmtpPort(1),
		"from":       settingService.SetSmtpFrom("panel@example.com"),
		"to":         settingService.SetSmtpTo("admin@example.com"),
		"encryption": settingService.SetSmtpEncryptionType("none"),
	} {
		if err != nil {
			t.Fatalf("set %s: %v", name, err)
		}
	}

	sub := NewSubscriber(settingService, NewEmailService(settingService))
	// A long cooldown, so a spent token stays spent for the whole test.
	sub.limiter = eventbus.NewRateLimiter(time.Hour)

	quiet := eventbus.Event{
		Type:   eventbus.EventCPUHigh,
		Source: "panel",
		Data:   &eventbus.SystemMetricData{Percent: 12},
	}
	sub.HandleEvent(quiet)

	// The spike the operator actually configured the alert for.
	if !sub.limiter.Allow(eventbus.EventCPUHigh, eventbus.LimiterKey(quiet)) {
		t.Fatal("a reading of 12% against a threshold of 80% consumed the alert's allowance, so the spike behind it is dropped with no mail sent and nothing logged")
	}
}
