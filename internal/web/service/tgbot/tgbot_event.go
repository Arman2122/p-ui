package tgbot

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/web/service"
)

var cachedHostname string

func getHostname() string {
	if cachedHostname != "" {
		return cachedHostname
	}
	h, err := os.Hostname()
	if err != nil {
		cachedHostname = "unknown"
	} else {
		cachedHostname = h
	}
	return cachedHostname
}

var tgEventLimiter = eventbus.NewRateLimiter(1 * time.Minute)

// HandleEvent is the eventbus subscriber callback. It formats incoming events
// as Telegram messages and sends them to all admin chats.
func (t *Tgbot) HandleEvent(e eventbus.Event) {
	if !t.isEventEnabled(e.Type) {
		return
	}
	msg := t.formatEventMessage(e)
	if msg == "" {
		return
	}
	if !tgEventLimiter.Allow(e.Type, eventbus.LimiterKey(e)) {
		return
	}
	t.SendMsgToTgbotAdmins(msg)
	if data, ok := e.Data.(*eventbus.ClientDeviceLimitData); ok && e.Type == eventbus.EventClientDeviceLimit {
		t.tellClientDeviceLimit(data)
	}
}

// tellClientDeviceLimit tells the client themselves, when the panel knows their
// Telegram id: a blocked device that looks like a network outage is a support ticket.
func (t *Tgbot) tellClientDeviceLimit(data *eventbus.ClientDeviceLimitData) {
	if data.Action == "reported" {
		return
	}
	_, client, err := t.inboundService.GetClientByEmail(data.Email)
	if err != nil || client == nil || client.TgID == 0 {
		return
	}
	msg := t.I18nBot("tgbot.messages.deviceLimitClient", "Limit=="+strconv.Itoa(data.Limit))
	switch data.Action {
	case "cut":
		msg += "\n" + t.I18nBot("tgbot.messages.deviceLimitClientCut", "IP=="+data.IP)
	case "ban":
		msg += "\n" + t.I18nBot("tgbot.messages.deviceLimitClientBan", "IP=="+data.IP, "Until=="+untilText(data.Until))
	}
	t.SendMsgToTgbot(client.TgID, msg)
}

func untilText(until int64) string {
	return time.Unix(until, 0).Format("2006-01-02 15:04:05")
}

// deviceLimitOutcome says what became of the device.
func (t *Tgbot) deviceLimitOutcome(data *eventbus.ClientDeviceLimitData) string {
	where := ""
	if data.Where != "" {
		where = t.I18nBot("tgbot.messages.eventDeviceLimitOnNode", "Node=="+data.Where)
	}
	switch data.Action {
	case "cut":
		return t.I18nBot("tgbot.messages.eventDeviceLimitCut", "IP=="+data.IP, "Where=="+where)
	case "ban":
		return t.I18nBot("tgbot.messages.eventDeviceLimitBan", "IP=="+data.IP, "Where=="+where, "Until=="+untilText(data.Until))
	default:
		return t.I18nBot("tgbot.messages.eventDeviceLimitReported", "IP=="+data.IP)
	}
}

func (t *Tgbot) isEventEnabled(eventType eventbus.EventType) bool {
	events, err := t.settingService.GetTgEnabledEvents()
	if err != nil || events == "" {
		return false
	}
	for e := range strings.SplitSeq(events, ",") {
		if strings.TrimSpace(e) == string(eventType) {
			return true
		}
	}
	return false
}

func (t *Tgbot) formatEventMessage(e eventbus.Event) string {
	host := getHostname()
	header := fmt.Sprintf("<b>📡 %s</b>\n", host)

	switch e.Type {
	case eventbus.EventResellerBudget:
		data, ok := e.Data.(service.ResellerNotice)
		if !ok || data.Username == "" {
			return ""
		}
		key, params := data.BotMessage()
		if key == "" {
			return ""
		}
		return header + "👥 " + t.I18nBot("tgbot.messages.eventResellerTitle", "Name=="+data.Name, "Username=="+data.Username) + "\n" +
			t.I18nBot(key, params...)

	case eventbus.EventClientPlanDepleted:
		data, ok := e.Data.(service.PlanDepletionNotice)
		if !ok || data.Email == "" || data.QuotaBytes <= 0 {
			return ""
		}
		return header + t.I18nBot("tgbot.messages.eventPlanDepletedTitle") + "\n" +
			t.I18nBot("tgbot.messages.eventPlanDepletedBody",
				"Email=="+data.Email, "Plan=="+data.PlanName,
				"Used=="+strconv.FormatInt(data.UsedBytes, 10), "Quota=="+strconv.FormatInt(data.QuotaBytes, 10))

	case eventbus.EventOutboundDown:
		msg := header + t.I18nBot("tgbot.messages.eventOutboundDown",
			"Tag=="+e.Source)
		if data, ok := e.Data.(*eventbus.OutboundHealthData); ok {
			if data.Error != "" {
				msg += "\n" + t.I18nBot("tgbot.messages.eventErrorDetail",
					"Error=="+data.Error)
			}
			if data.Delay > 0 {
				msg += "\n" + t.I18nBot("tgbot.messages.eventDelayDetail",
					"Delay=="+fmt.Sprintf("%d", data.Delay))
			}
		}
		return msg

	case eventbus.EventOutboundUp:
		msg := header + t.I18nBot("tgbot.messages.eventOutboundUp",
			"Tag=="+e.Source)
		if data, ok := e.Data.(*eventbus.OutboundHealthData); ok && data.Delay > 0 {
			msg += "\n" + t.I18nBot("tgbot.messages.eventDelayDetail",
				"Delay=="+fmt.Sprintf("%d", data.Delay))
		}
		return msg

	case eventbus.EventClientDeviceLimit:
		data, ok := e.Data.(*eventbus.ClientDeviceLimitData)
		if !ok {
			return ""
		}
		msg := header + "🚫 " + t.I18nBot("tgbot.messages.eventDeviceLimit",
			"Email=="+data.Email, "Devices=="+strconv.Itoa(data.Devices), "Limit=="+strconv.Itoa(data.Limit))
		return msg + "\n" + t.deviceLimitOutcome(data)

	case eventbus.EventCoreCrash:
		msg := header + "🔥 " + t.I18nBot("tgbot.messages.eventCoreCrash", "Core=="+coreCrashName(e))
		if data, ok := e.Data.(*eventbus.CoreCrashData); ok && data.Err != "" {
			msg += "\n" + t.I18nBot("tgbot.messages.eventXrayCrashError", "Error=="+data.Err)
		}
		return msg

	case eventbus.EventCoreUp:
		return header + "✅ " + t.I18nBot("tgbot.messages.eventCoreUp", "Core=="+coreCrashName(e))

	case eventbus.EventXrayCrash:
		errStr := ""
		if e.Data != nil {
			errStr = fmt.Sprint(e.Data)
		}
		msg := header + "🔥 " + t.I18nBot("tgbot.messages.eventXrayCrash")
		if errStr != "" {
			msg += "\n" + t.I18nBot("tgbot.messages.eventXrayCrashError", "Error=="+errStr)
		}
		return msg

	case eventbus.EventNodeDown:
		msg := header + "🔴 " + t.I18nBot("tgbot.messages.eventNodeDown", "Name=="+e.Source)
		if data, ok := e.Data.(*eventbus.NodeHealthData); ok && data.XrayError != "" {
			msg += "\n" + t.I18nBot("tgbot.messages.eventErrorDetail", "Error=="+data.XrayError)
		}
		return msg

	case eventbus.EventNodeUp:
		msg := header + "🟢 " + t.I18nBot("tgbot.messages.eventNodeUp", "Name=="+e.Source)
		if data, ok := e.Data.(*eventbus.NodeHealthData); ok && data.LatencyMs > 0 {
			msg += "\n" + t.I18nBot("tgbot.messages.eventDelayDetail", "Delay=="+fmt.Sprintf("%d", data.LatencyMs))
		}
		return msg

	case eventbus.EventCPUHigh:
		if data, ok := e.Data.(*eventbus.SystemMetricData); ok {
			tgCpu, err := t.settingService.GetTgCpu()
			if err != nil || tgCpu <= 0 || data.Percent <= float64(tgCpu) {
				return ""
			}
			return header + "🔴 " + t.I18nBot("tgbot.messages.cpuThreshold",
				"Percent=="+strconv.FormatFloat(data.Percent, 'f', 2, 64),
				"Threshold=="+strconv.Itoa(tgCpu))
		}
		return ""

	case eventbus.EventMemoryHigh:
		if data, ok := e.Data.(*eventbus.SystemMetricData); ok {
			tgMemory, err := t.settingService.GetTgMemory()
			if err != nil || tgMemory <= 0 || data.Percent <= float64(tgMemory) {
				return ""
			}
			return header + "🔴 " + t.I18nBot("tgbot.messages.memoryThreshold",
				"Percent=="+strconv.FormatFloat(data.Percent, 'f', 2, 64),
				"Threshold=="+strconv.Itoa(tgMemory))
		}
		return ""

	case eventbus.EventLoginAttempt:
		if data, ok := e.Data.(*eventbus.LoginEventData); ok {
			if data.Status == "success" {
				msg := t.I18nBot("tgbot.messages.loginSuccess")
				msg += t.I18nBot("tgbot.messages.hostname", "Hostname=="+host)
				msg += t.I18nBot("tgbot.messages.username", "Username=="+data.Username)
				msg += t.I18nBot("tgbot.messages.ip", "IP=="+data.IP)
				msg += t.I18nBot("tgbot.messages.time", "Time=="+data.Time)
				return msg
			}
			msg := t.I18nBot("tgbot.messages.loginFailed")
			msg += t.I18nBot("tgbot.messages.hostname", "Hostname=="+host)
			if data.Reason != "" {
				msg += t.I18nBot("tgbot.messages.reason", "Reason=="+data.Reason)
			}
			msg += t.I18nBot("tgbot.messages.username", "Username=="+data.Username)
			msg += t.I18nBot("tgbot.messages.ip", "IP=="+data.IP)
			msg += t.I18nBot("tgbot.messages.time", "Time=="+data.Time)
			return msg
		}
		return header + t.I18nBot("tgbot.messages.eventLoginFallback", "Source=="+e.Source)
	}

	return ""
}

// coreCrashName is what an operator sees a dead daemon called: the source the
// publisher set, or the bare core id when it set none.
func coreCrashName(e eventbus.Event) string {
	if e.Source != "" {
		return e.Source
	}
	if data, ok := e.Data.(*eventbus.CoreCrashData); ok {
		return data.CoreID
	}
	return "core"
}
