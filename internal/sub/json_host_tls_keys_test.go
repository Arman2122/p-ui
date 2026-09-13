package sub

import (
	"encoding/json"
	"testing"

	"github.com/xtls/xray-core/infra/conf"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A Host row's pin, ECH config and name check were written under
tlsSettings.settings, a member xray's TLSConfig does not have, after the
renderer had already flattened the inbound's own bag: the JSON profile carried
the relay's serverName with no pin, and the raw link (right) and the JSON
subscription (wrong) disagreed. They land where xray reads them, as it reads
them: the pins joined by commas.
*/
func TestJsonSubscriptionCarriesTheHostRowsTLSKeysWhereXrayReadsThem(t *testing.T) {
	subReq := NewSubService("")
	js := NewSubJsonService("", "", "", subReq)
	inbound := &model.Inbound{
		Listen: "203.0.113.21", Port: 443, Protocol: model.VLESS, Remark: "relayed",
		Settings: `{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"user"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"origin.example","settings":{"fingerprint":"chrome"}},` +
			`"externalProxy":[{"dest":"relay.example","port":8443,"sni":"relay.example","pinnedPeerCertSha256":["aa11bb22"],"echConfigList":"AEX+DQBB","verifyPeerCertByName":"relay.example"}]}`,
	}
	configs := js.getConfig(subReq, inbound, model.Client{ID: "11111111-2222-4333-8444-555555555555", Email: "user"}, "req.example")
	if len(configs) != 1 {
		t.Fatalf("got %d configs for one host row", len(configs))
	}
	var cfg struct {
		Outbounds []struct {
			Tag            string `json:"tag"`
			StreamSettings struct {
				Security    string          `json:"security"`
				TLSSettings json.RawMessage `json:"tlsSettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(configs[0], &cfg); err != nil || len(cfg.Outbounds) == 0 {
		t.Fatalf("config: %v\n%s", err, configs[0])
	}
	var tlsConf conf.TLSConfig
	if err := json.Unmarshal(cfg.Outbounds[0].StreamSettings.TLSSettings, &tlsConf); err != nil {
		t.Fatalf("xray's TLSConfig refused the rendered tlsSettings: %v\n%s", err, cfg.Outbounds[0].StreamSettings.TLSSettings)
	}
	if tlsConf.ServerName != "relay.example" || tlsConf.PinnedPeerCertSha256 != "aa11bb22" || tlsConf.ECHConfigList != "AEX+DQBB" || tlsConf.VerifyPeerCertByName != "relay.example" {
		t.Fatalf("xray reads serverName=%q pins=%q ech=%q vcn=%q; the host row's TLS keys were dropped:\n%s",
			tlsConf.ServerName, tlsConf.PinnedPeerCertSha256, tlsConf.ECHConfigList, tlsConf.VerifyPeerCertByName, cfg.Outbounds[0].StreamSettings.TLSSettings)
	}
}
