package nodeconfig

import (
	"encoding/json"
	"slices"
	"strings"
)

func plainXrayBase(base map[string]json.RawMessage) bool {
	if len(base) != 1 {
		return false
	}
	var outbounds []struct {
		Tag              string `json:"tag"`
		OutboundProtocol string `json:"protocol"`
	}
	return strictSettings(string(base["outbounds"]), &outbounds) == nil && len(outbounds) == 1 && outbounds[0].OutboundProtocol == "freedom"
}

func xrayBaseReferences(base map[string]json.RawMessage) error {
	var outbounds []struct {
		Tag              string `json:"tag"`
		OutboundProtocol string `json:"protocol"`
		ProxySettings    struct {
			Tag string `json:"tag"`
		} `json:"proxySettings"`
		StreamSettings struct {
			Sockopt struct {
				DialerProxy string `json:"dialerProxy"`
			} `json:"sockopt"`
		} `json:"streamSettings"`
	}
	if json.Unmarshal(base["outbounds"], &outbounds) != nil || len(outbounds) == 0 || len(outbounds) > 4096 {
		return ErrInvalid
	}
	dependencies := map[string][]string{}
	for _, outbound := range outbounds {
		if !compilerName.MatchString(outbound.Tag) {
			return ErrInvalid
		}
		if _, duplicate := dependencies[outbound.Tag]; duplicate {
			return ErrConflict
		}
		if !slices.Contains([]string{"freedom", "blackhole", "vmess", "vless", "trojan", "shadowsocks", "http", "socks"}, outbound.OutboundProtocol) {
			return ErrUnsupported
		}
		dependencies[outbound.Tag] = nil
		for _, target := range []string{outbound.ProxySettings.Tag, outbound.StreamSettings.Sockopt.DialerProxy} {
			if target != "" {
				dependencies[outbound.Tag] = append(dependencies[outbound.Tag], target)
			}
		}
	}
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(tag string) error {
		next, known := dependencies[tag]
		if !known {
			return ErrDependency
		}
		if state[tag] == 1 {
			return ErrConflict
		}
		if state[tag] == 2 {
			return nil
		}
		state[tag] = 1
		for _, target := range next {
			if err := visit(target); err != nil {
				return err
			}
		}
		state[tag] = 2
		return nil
	}
	for tag := range dependencies {
		if err := visit(tag); err != nil {
			return err
		}
	}
	var routing struct {
		Rules []struct {
			OutboundTag string `json:"outboundTag"`
			BalancerTag string `json:"balancerTag"`
		} `json:"rules"`
		Balancers []struct {
			Tag         string   `json:"tag"`
			Selector    []string `json:"selector"`
			FallbackTag string   `json:"fallbackTag"`
		} `json:"balancers"`
	}
	if raw := base["routing"]; len(raw) != 0 && json.Unmarshal(raw, &routing) != nil {
		return ErrInvalid
	}
	balancers := map[string]bool{}
	for _, balancer := range routing.Balancers {
		if !compilerName.MatchString(balancer.Tag) || balancers[balancer.Tag] || len(balancer.Selector) == 0 {
			return ErrInvalid
		}
		if balancer.FallbackTag != "" {
			if _, ok := dependencies[balancer.FallbackTag]; !ok {
				return ErrDependency
			}
		}
		for _, prefix := range balancer.Selector {
			matched := false
			for tag := range dependencies {
				if strings.HasPrefix(tag, prefix) {
					matched = true
				}
			}
			if prefix == "" || !matched {
				return ErrDependency
			}
		}
		balancers[balancer.Tag] = true
	}
	for _, rule := range routing.Rules {
		if (rule.OutboundTag == "") == (rule.BalancerTag == "") {
			return ErrInvalid
		}
		if rule.OutboundTag != "" {
			if _, ok := dependencies[rule.OutboundTag]; !ok {
				return ErrDependency
			}
		}
		if rule.BalancerTag != "" && !balancers[rule.BalancerTag] {
			return ErrDependency
		}
	}
	return nil
}
