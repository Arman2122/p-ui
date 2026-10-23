package database

import (
	"strconv"
	"strings"
)

// NormalizeXrayOutboundSettings preserves the core's effective settings while
// replacing deprecated keys and spelling a DNS query type zero unambiguously.
func NormalizeXrayOutboundSettings(raw string) (string, bool, error) {
	return rewriteTemplateOutbounds(raw, rewriteOutboundCoreSettings)
}

func rewriteOutboundCoreSettings(obj map[string]any) bool {
	protocol, _ := obj["protocol"].(string)
	switch strings.ToLower(protocol) {
	case "freedom":
		return rewriteFreedomStrategy(obj)
	case "dns":
		settings, _ := obj["settings"].(map[string]any)
		changed := rewriteLegacyDNSRules(settings)
		rules, _ := settings["rules"].([]any)
		for _, entry := range rules {
			rule, _ := entry.(map[string]any)
			if qType, ok := rule["qType"].(float64); ok && qType == 0 {
				rule["qType"] = "0"
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func rewriteFreedomStrategy(obj map[string]any) bool {
	settings, _ := obj["settings"].(map[string]any)
	_, rootPresent := obj["targetStrategy"]
	_, targetPresent := settings["targetStrategy"]
	_, domainPresent := settings["domainStrategy"]
	if !rootPresent && !targetPresent && !domainPresent {
		return false
	}
	for _, value := range []any{obj["targetStrategy"], settings["targetStrategy"], settings["domainStrategy"]} {
		if value != nil && !validFreedomStrategy(value) {
			return false
		}
	}
	strategy, _ := obj["targetStrategy"].(string)
	if strategy == "" || strings.EqualFold(strategy, "AsIs") {
		strategy, _ = settings["targetStrategy"].(string)
		if strategy == "" {
			strategy, _ = settings["domainStrategy"].(string)
		}
	}
	if strategy != "" && !strings.EqualFold(strategy, "AsIs") {
		stream, _ := obj["streamSettings"].(map[string]any)
		if stream == nil {
			stream = map[string]any{}
			obj["streamSettings"] = stream
		}
		sockopt, _ := stream["sockopt"].(map[string]any)
		if sockopt == nil {
			sockopt = map[string]any{}
			stream["sockopt"] = sockopt
		}
		sockopt["domainStrategy"] = strategy
	}
	delete(obj, "targetStrategy")
	delete(settings, "targetStrategy")
	delete(settings, "domainStrategy")
	return true
}

func validFreedomStrategy(value any) bool {
	strategy, ok := value.(string)
	if !ok {
		return false
	}
	switch strings.ToLower(strategy) {
	case "", "asis", "useip", "useipv4", "useipv6", "useipv4v6", "useipv6v4",
		"forceip", "forceipv4", "forceipv6", "forceipv4v6", "forceipv6v4":
		return true
	default:
		return false
	}
}

func rewriteLegacyDNSRules(settings map[string]any) bool {
	if settings["nonIPQuery"] == nil && settings["blockTypes"] == nil {
		return false
	}
	if settings["rules"] == nil {
		mode := "reject"
		if value := settings["nonIPQuery"]; value != nil && value != "" {
			var ok bool
			mode, ok = value.(string)
			if !ok || (mode != "reject" && mode != "drop" && mode != "skip") {
				return false
			}
		}
		types, ok := settings["blockTypes"].([]any)
		if settings["blockTypes"] != nil && !ok {
			return false
		}
		queryTypes := make([]string, 0, len(types))
		for _, value := range types {
			qType, ok := value.(float64)
			if !ok || qType < 0 || qType > 65535 || qType != float64(int(qType)) {
				return false
			}
			queryTypes = append(queryTypes, strconv.Itoa(int(qType)))
		}
		rules := make([]any, 0, 3)
		if len(queryTypes) > 0 {
			rule := map[string]any{"action": "drop", "qType": strings.Join(queryTypes, ",")}
			if mode == "reject" {
				rule["action"], rule["rCode"] = "return", 5
			}
			rules = append(rules, rule)
		}
		rules = append(rules, map[string]any{"action": "hijack", "qType": "1,28"})
		fallback := map[string]any{"action": "direct"}
		switch mode {
		case "reject":
			fallback["action"], fallback["rCode"] = "return", 5
		case "drop":
			fallback["action"] = "drop"
		}
		settings["rules"] = append(rules, fallback)
	}
	delete(settings, "nonIPQuery")
	delete(settings, "blockTypes")
	return true
}
