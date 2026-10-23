package nodeconfig

import (
	"crypto/tls"
	"encoding/json"
	"slices"
	"strings"
)

func (r *renderer) streamCertificate(inbound Inbound, runtime string) ([]string, []string, error) {
	certificate, err := r.asset(inbound, runtime, "certificate", AssetCertificate, AssetEmbedded)
	if err != nil {
		return nil, nil, err
	}
	key, err := r.asset(inbound, runtime, "privateKey", AssetPrivateKey, AssetEmbedded)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tls.X509KeyPair(certificate.Data, key.Data); err != nil {
		return nil, nil, issue(inbound, "", "certificate", ErrInvalid)
	}
	return strings.Split(strings.TrimSpace(string(certificate.Data)), "\n"), strings.Split(strings.TrimSpace(string(key.Data)), "\n"), nil
}

func streamBase(input Input, runtime string, allowed ...string) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	for _, base := range input.Bases {
		if base.Core != runtime {
			continue
		}
		if base.RuntimeID != runtime || result != nil || len(base.Assets) != 0 || len(base.Configuration) == 0 || len(base.Configuration) > 32<<20 {
			return nil, ErrDependency
		}
		if json.Unmarshal(base.Configuration, &result) != nil || result == nil {
			return nil, ErrInvalid
		}
		for field := range result {
			if !slices.Contains(allowed, field) {
				return nil, ErrUnsupported
			}
		}
		if externalFileReference(result) {
			return nil, ErrDependency
		}
	}
	return result, nil
}

func externalFileReference(value any) bool {
	switch value := value.(type) {
	case map[string]json.RawMessage:
		var object map[string]any
		data, _ := json.Marshal(value)
		if json.Unmarshal(data, &object) != nil {
			return true
		}
		return externalFileReference(object)
	case map[string]any:
		for field, child := range value {
			name := strings.ToLower(field)
			if text, ok := child.(string); ok && text != "" && (strings.HasSuffix(name, "file") || strings.HasSuffix(name, "_path") || name == "geofile") {
				return true
			}
			if externalFileReference(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if externalFileReference(child) {
				return true
			}
		}
	case string:
		if strings.HasPrefix(value, "ext:") || strings.HasPrefix(value, "ext-ip:") || strings.HasPrefix(value, "geoip:") || strings.HasPrefix(value, "geosite:") {
			return true
		}
	}
	return false
}
