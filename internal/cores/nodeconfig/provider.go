package nodeconfig

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"github.com/xtls/xray-core/infra/conf"
)

func ValidateXrayProviderOutbounds(data []byte) error {
	if len(data) == 0 || len(data) > 8<<20 {
		return ErrInvalid
	}
	var entries []json.RawMessage
	if json.Unmarshal(data, &entries) != nil || entries == nil || len(entries) > 4096 {
		return ErrInvalid
	}
	tags := map[string]bool{}
	for _, raw := range entries {
		var config conf.OutboundDetourConfig
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || !compilerName.MatchString(config.Tag) || tags[config.Tag] {
			return ErrInvalid
		}
		if !slices.Contains([]string{"vmess", "vless", "trojan", "shadowsocks"}, config.Protocol) {
			return ErrUnsupported
		}
		var object any
		if json.Unmarshal(raw, &object) != nil || providerFileReference(object) {
			return ErrDependency
		}
		if _, err := config.Build(); err != nil {
			return ErrInvalid
		}
		tags[config.Tag] = true
	}
	return nil
}

func providerFileReference(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, nested := range value {
			if slices.ContainsFunc([]string{"certificateFile", "keyFile", "certFile", "secretKeyFile"}, func(field string) bool { return strings.EqualFold(key, field) }) || providerFileReference(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range value {
			if providerFileReference(nested) {
				return true
			}
		}
	}
	return false
}
