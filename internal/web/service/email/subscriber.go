package email

import (
	"fmt"
	"html"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/locale"
	"github.com/Arman2122/p-ui/internal/web/service"
)

// Subscriber handles event bus messages and sends email notifications.
type Subscriber struct {
	settingService service.SettingService
	emailService   *EmailService
	limiter        *eventbus.RateLimiter
}

// NewSubscriber creates a new email event subscriber.
func NewSubscriber(settingService service.SettingService, emailService *EmailService) *Subscriber {
	return &Subscriber{
		settingService: settingService,
		emailService:   emailService,
		limiter:        eventbus.NewRateLimiter(1 * time.Minute),
	}
}

// HandleEvent is the eventbus subscriber callback.
func (s *Subscriber) HandleEvent(e eventbus.Event) {
	if on, err := s.settingService.GetSmtpEnable(); err != nil || !on {
		return
	}
	if !s.isEventEnabled(e.Type) {
		return
	}
	/*
		Formatted first, rate-limited second.

		formatMessage is where cpu.high and memory.high compare the reading
		against the operator's threshold and return nothing when it is below it.
		Spending the limiter's token before that meant a quiet minute consumed the
		allowance, and the watchdogs publish on every run at the same one-minute
		cadence as the cooldown — so an alert that really did cross the threshold
		was dropped because the reading before it had not. F365.
	*/
	subject, body := s.formatMessage(e)
	if subject == "" {
		return
	}
	if !s.limiter.Allow(e.Type, eventbus.LimiterKey(e)) {
		return
	}
	if err := s.emailService.Send(subject, body); err != nil {
		logger.Warning("email subscriber: send failed:", err)
	}
}

func (s *Subscriber) isEventEnabled(t eventbus.EventType) bool {
	events, err := s.settingService.GetSmtpEnabledEvents()
	if err != nil || events == "" {
		return false
	}
	for e := range strings.SplitSeq(events, ",") {
		if strings.TrimSpace(e) == string(t) {
			return true
		}
	}
	return false
}

func i18n(key string, params ...string) string {
	return locale.I18n(locale.Bot, key, params...)
}

func (s *Subscriber) formatMessage(e eventbus.Event) (subject, body string) {
	h, _ := hostname()
	host := h
	ts := e.Timestamp.Format("2006-01-02 15:04:05")

	wrap := func(title, content string) string {
		// Strip newlines from title to prevent broken HTML
		title = strings.ReplaceAll(title, "\r\n", "")
		title = strings.ReplaceAll(title, "\n", "")
		return fmt.Sprintf(`<html><body style="font-family:monospace;font-size:14px;color:#333">
<h2 style="color:#555;border-bottom:1px solid #ddd;padding-bottom:8px">📡 %s %s</h2>
%s
<p style="color:#999;font-size:12px;margin-top:20px">%s</p>
</body></html>`, html.EscapeString(host), html.EscapeString(title), content, i18n("tgbot.messages.time", "Time=="+ts))
	}

	// kv escapes its value: a node name, an outbound tag or an xray error is
	// text, and one carrying `<` used to garble the mail. kvRaw is for the
	// status lines below, whose value is markup this file wrote itself. F360.
	kv := func(key, val string) string {
		return fmt.Sprintf("<p><b>%s:</b> %s</p>", html.EscapeString(key), html.EscapeString(val))
	}
	kvRaw := func(key, markup string) string {
		return fmt.Sprintf("<p><b>%s:</b> %s</p>", html.EscapeString(key), markup)
	}

	switch e.Type {
	case eventbus.EventOutboundDown:
		subject = host + " " + i18n("tgbot.messages.eventOutboundDown", "Tag=="+e.Source)
		content := kvRaw(i18n("email.labelStatus"), `<span style="color:red">`+i18n("email.statusDown")+`</span>`)
		content += kv(i18n("email.labelOutbound"), e.Source)
		if data, ok := e.Data.(*eventbus.OutboundHealthData); ok {
			if data.Error != "" {
				content += kv(i18n("email.labelError"), data.Error)
			}
			if data.Delay > 0 {
				content += kv(i18n("email.labelDelay"), fmt.Sprintf("%dms", data.Delay))
			}
		}
		body = wrap(i18n("tgbot.messages.eventOutboundDown", "Tag=="+e.Source), content)

	case eventbus.EventOutboundUp:
		subject = host + " " + i18n("tgbot.messages.eventOutboundUp", "Tag=="+e.Source)
		content := kvRaw(i18n("email.labelStatus"), `<span style="color:green">`+i18n("email.statusUp")+`</span>`)
		content += kv(i18n("email.labelOutbound"), e.Source)
		if data, ok := e.Data.(*eventbus.OutboundHealthData); ok && data.Delay > 0 {
			content += kv(i18n("email.labelDelay"), fmt.Sprintf("%dms", data.Delay))
		}
		body = wrap(i18n("tgbot.messages.eventOutboundUp", "Tag=="+e.Source), content)

	case eventbus.EventClientDeviceLimit:
		data, ok := e.Data.(*eventbus.ClientDeviceLimitData)
		if !ok {
			return "", ""
		}
		title := i18n("tgbot.messages.eventDeviceLimit",
			"Email=="+data.Email, "Devices=="+strconv.Itoa(data.Devices), "Limit=="+strconv.Itoa(data.Limit))
		subject = host + " " + title
		content := kv(i18n("email.labelIP"), data.IP)
		switch data.Action {
		case "cut":
			content += kv(i18n("email.labelAction"), i18n("email.deviceLimitCut"))
		case "ban":
			content += kv(i18n("email.labelAction"), i18n("email.deviceLimitBan"))
			content += kv(i18n("email.labelUntil"), time.Unix(data.Until, 0).Format("2006-01-02 15:04:05"))
		default:
			content += kv(i18n("email.labelAction"), i18n("email.deviceLimitReported"))
		}
		if data.Where != "" {
			content += kv(i18n("email.labelNode"), data.Where)
		}
		body = wrap(title, content)

	case eventbus.EventCoreCrash:
		name := e.Source
		errText := ""
		if data, ok := e.Data.(*eventbus.CoreCrashData); ok {
			if name == "" {
				name = data.CoreID
			}
			errText = data.Err
		}
		subject = host + " " + i18n("tgbot.messages.eventCoreCrash", "Core=="+name)
		content := kvRaw(i18n("email.labelStatus"), `<span style="color:red">`+i18n("email.statusCrashed")+`</span>`)
		if errText != "" {
			content += kv(i18n("email.labelError"), errText)
		}
		body = wrap(i18n("tgbot.messages.eventCoreCrash", "Core=="+name), content)

	case eventbus.EventCoreUp:
		name := e.Source
		if data, ok := e.Data.(*eventbus.CoreCrashData); ok && name == "" {
			name = data.CoreID
		}
		subject = host + " " + i18n("tgbot.messages.eventCoreUp", "Core=="+name)
		content := kvRaw(i18n("email.labelStatus"), `<span style="color:green">`+i18n("email.statusUp")+`</span>`)
		body = wrap(i18n("tgbot.messages.eventCoreUp", "Core=="+name), content)

	case eventbus.EventXrayCrash:
		subject = host + " " + i18n("tgbot.messages.eventXrayCrash")
		content := kvRaw(i18n("email.labelStatus"), `<span style="color:red">`+i18n("email.statusCrashed")+`</span>`)
		if e.Data != nil {
			content += kv(i18n("email.labelError"), fmt.Sprint(e.Data))
		}
		body = wrap(i18n("tgbot.messages.eventXrayCrash"), content)

	case eventbus.EventNodeDown:
		subject = host + " " + i18n("tgbot.messages.eventNodeDown", "Name=="+e.Source)
		content := kvRaw(i18n("email.labelStatus"), `<span style="color:red">`+i18n("email.statusDown")+`</span>`)
		content += kv(i18n("email.labelNode"), e.Source)
		if data, ok := e.Data.(*eventbus.NodeHealthData); ok && data.XrayError != "" {
			content += kv(i18n("email.labelError"), data.XrayError)
		}
		body = wrap(i18n("tgbot.messages.eventNodeDown", "Name=="+e.Source), content)

	case eventbus.EventNodeUp:
		subject = host + " " + i18n("tgbot.messages.eventNodeUp", "Name=="+e.Source)
		content := kvRaw(i18n("email.labelStatus"), `<span style="color:green">`+i18n("email.statusUp")+`</span>`)
		content += kv(i18n("email.labelNode"), e.Source)
		if data, ok := e.Data.(*eventbus.NodeHealthData); ok && data.LatencyMs > 0 {
			content += kv(i18n("email.labelDelay"), fmt.Sprintf("%dms", data.LatencyMs))
		}
		body = wrap(i18n("tgbot.messages.eventNodeUp", "Name=="+e.Source), content)

	case eventbus.EventCPUHigh:
		if data, ok := e.Data.(*eventbus.SystemMetricData); ok {
			smtpCpu, err := s.settingService.GetSmtpCpu()
			if err != nil || smtpCpu <= 0 || data.Percent <= float64(smtpCpu) {
				return
			}
			subject = host + " " + i18n("tgbot.messages.cpuThreshold",
				"Percent=="+strconv.FormatFloat(data.Percent, 'f', 2, 64),
				"Threshold=="+fmt.Sprintf("%d", smtpCpu))
			content := kvRaw(i18n("email.labelStatus"), `<span style="color:orange">`+i18n("email.statusHigh")+`</span>`)
			body = wrap(subject, content)
		}

	case eventbus.EventMemoryHigh:
		if data, ok := e.Data.(*eventbus.SystemMetricData); ok {
			smtpMemory, err := s.settingService.GetSmtpMemory()
			if err != nil || smtpMemory <= 0 || data.Percent <= float64(smtpMemory) {
				return
			}
			subject = host + " " + i18n("tgbot.messages.memoryThreshold",
				"Percent=="+strconv.FormatFloat(data.Percent, 'f', 2, 64),
				"Threshold=="+fmt.Sprintf("%d", smtpMemory))
			content := kvRaw(i18n("email.labelStatus"), `<span style="color:orange">`+i18n("email.statusHigh")+`</span>`)
			body = wrap(subject, content)
		}

	case eventbus.EventLoginAttempt:
		if data, ok := e.Data.(*eventbus.LoginEventData); ok {
			if data.Status == "success" {
				subject = host + " " + i18n("tgbot.messages.loginSuccess")
				content := kvRaw(i18n("email.labelStatus"), `<span style="color:green">`+i18n("email.statusSuccess")+`</span>`)
				content += kv(i18n("email.labelUsername"), data.Username)
				content += kv(i18n("email.labelIP"), data.IP)
				body = wrap(i18n("tgbot.messages.loginSuccess"), content)
			} else {
				subject = host + " " + i18n("tgbot.messages.loginFailed")
				content := kvRaw(i18n("email.labelStatus"), `<span style="color:red">`+i18n("email.statusFailed")+`</span>`)
				if data.Reason != "" {
					content += kv(i18n("email.labelReason"), data.Reason)
				}
				content += kv(i18n("email.labelUsername"), data.Username)
				content += kv(i18n("email.labelIP"), data.IP)
				body = wrap(i18n("tgbot.messages.loginFailed"), content)
			}
		} else {
			subject = host + " " + i18n("tgbot.messages.loginFailed")
			content := kvRaw(i18n("email.labelStatus"), `<span style="color:red">`+i18n("email.statusFailed")+`</span>`)
			content += kv(i18n("email.labelSource"), e.Source)
			body = wrap(i18n("tgbot.messages.loginFailed"), content)
		}
	}

	return
}

func hostname() (string, error) {
	return os.Hostname()
}
