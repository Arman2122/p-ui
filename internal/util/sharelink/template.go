package sharelink

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/goccy/go-json"

	"github.com/Arman2122/p-ui/internal/core"
)

var ErrUnsupported = errors.New("client share format is unsupported")

type XrayTemplate struct {
	Scheme, UserInfo, Network, Security string
	Params                              map[string]string
	Object                              map[string]any
}

func (t XrayTemplate) Render(host string, port int, remark string) string {
	if t.Object != nil {
		obj := make(map[string]any, len(t.Object)+3)
		for key, value := range t.Object {
			obj[key] = value
		}
		obj["add"], obj["port"], obj["ps"] = host, port, remark
		return BuildVmessLink(obj)
	}
	return BuildLinkWithParams(fmt.Sprintf("%s://%s@%s", t.Scheme, t.UserInfo, JoinHostPort(host, port)), t.Params, remark)
}

func PrepareXray(inst core.Instance, user core.User, clientKey string) (XrayTemplate, error) {
	t := XrayTemplate{Scheme: string(inst.Kind), Params: map[string]string{}}
	stream, settings := map[string]any{}, map[string]any{}
	if inst.StreamSettings != "" && json.Unmarshal([]byte(inst.StreamSettings), &stream) != nil ||
		inst.Settings != "" && json.Unmarshal([]byte(inst.Settings), &settings) != nil {
		return t, errors.New("invalid client share settings")
	}
	t.Network, _ = stream["network"].(string)
	t.Security, _ = stream["security"].(string)
	uuid := core.CredString(user.Credentials, core.CredUUID)
	if alias := core.CredString(user.Credentials, "id"); alias != "" {
		if uuid != "" && uuid != alias {
			return t, errors.New("conflicting client share UUIDs")
		}
		uuid = alias
	}
	credential := func(name string) string { return core.CredString(user.Credentials, name) }
	if inst.Kind == "vmess" {
		t.Object = map[string]any{"v": "2", "type": "none", "id": uuid, "scy": NormalizeVmessSecurity(credential(core.CredSecurity)), "tls": t.Security}
		ApplyVmessNetworkParams(stream, t.Network, t.Object)
		if masks, ok := stream["finalmask"].(map[string]any); ok {
			ApplyFinalMaskObj(masks, t.Object)
		}
		if t.Security == "tls" {
			ApplyVmessTLSParams(stream, t.Object)
		}
		return t, nil
	}
	if inst.Kind == "hysteria" {
		t.Scheme, t.UserInfo = "hysteria2", EncodeUserinfo(credential(core.CredAuth))
		if version, _ := settings["version"].(float64); int(version) == 1 {
			t.Scheme = "hysteria"
		}
		t.Network, t.Security = "quic", "tls"
		ApplyShareTLSParams(stream, t.Params)
		if pins, ok := t.Params["pcs"]; ok {
			t.Params["pinSHA256"] = pins
			delete(t.Params, "pcs")
		}
		ApplySalamanderParams(stream, t.Params)
		if ports := HysteriaHopPorts(stream); ports != "" {
			t.Params["mport"] = ports
		}
		return t, nil
	}
	t.Params["type"] = t.Network
	ApplyShareNetworkParams(stream, t.Network, t.Params)
	if masks, ok := stream["finalmask"].(map[string]any); ok {
		ApplyFinalMaskParams(masks, t.Params)
	}
	switch inst.Kind {
	case "vless", "trojan":
		if inst.Kind == "vless" {
			t.UserInfo = uuid
			if encryption, ok := settings["encryption"].(string); ok {
				t.Params["encryption"] = encryption
			}
		} else {
			t.UserInfo = EncodeUserinfo(credential(core.CredPassword))
		}
		switch t.Security {
		case "tls":
			ApplyShareTLSParams(stream, t.Params)
		case "reality":
			ApplyShareRealityParams(stream, t.Params, clientKey)
		default:
			t.Params["security"] = "none"
		}
		if flow := credential("flow"); flow != "" && core.Can(core.CapTLSFlow, core.Facts{Protocol: string(inst.Kind), Stream: map[string]string{"network": t.Network, "security": t.Security}, Settings: core.Scalars(settings)}) {
			t.Params["flow"] = flow
		}
	case "shadowsocks":
		t.Scheme = "ss"
		method, _ := settings["method"].(string)
		if strings.HasPrefix(method, "2022") {
			password, _ := settings["password"].(string)
			t.UserInfo = fmt.Sprintf("%s:%s:%s", url.QueryEscape(method), url.QueryEscape(password), url.QueryEscape(credential(core.CredPassword)))
		} else {
			t.UserInfo = base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", method, credential(core.CredPassword)))
		}
		if t.Security == "tls" {
			ApplyShareTLSParams(stream, t.Params)
		}
		if t.Network == "tcp" && t.Params["headerType"] == "http" {
			host := t.Params["host"]
			for _, key := range []string{"type", "headerType", "host", "path"} {
				delete(t.Params, key)
			}
			t.Params["plugin"] = "obfs-local;obfs=http;obfs-host=" + host
		}
	default:
		return t, ErrUnsupported
	}
	return t, nil
}

func MTProto(host string, port int, secret string) string {
	return BuildLinkWithParams("tg://proxy", map[string]string{"server": host, "port": fmt.Sprint(port), "secret": secret}, "")
}
