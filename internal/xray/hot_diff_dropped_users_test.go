package xray

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/util/json_util"
)

func TestHotDiffDetectsDroppedCredentialsAcrossProtocols(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan", "hysteria", "shadowsocks"} {
		for _, tc := range []struct {
			name, clients string
			drops         bool
		}{
			{"drop", `[{"email":"a","id":"id-a","password":"pa"}]`, true},
			{"edit", `[{"email":"a","id":"id-a","password":"pa"},{"email":"b","id":"id-b","password":"pb","level":1}]`, false},
			{"rename", `[{"email":"a","id":"id-a","password":"pa"},{"email":"c","id":"id-b","password":"pb"}]`, true},
			{"add", `[{"email":"a","id":"id-a","password":"pa"},{"email":"b","id":"id-b","password":"pb"},{"email":"c","id":"id-c","password":"pc"}]`, false},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				config := func(clients string) *Config {
					cfg := makeHotConfig()
					cfg.InboundConfigs[1].Protocol = protocol
					cfg.InboundConfigs[1].Settings = json_util.RawMessage(`{"clients":` + clients + `}`)
					return cfg
				}
				old := config(`[{"email":"a","id":"id-a","password":"pa"},{"email":"b","id":"id-b","password":"pb"}]`)
				diff, ok := ComputeHotDiff(old, config(tc.clients))
				if !ok {
					t.Fatal("client change unexpectedly rejected hot diff")
				}
				if got := diff.DropsUsers(); got != tc.drops {
					t.Fatalf("DropsUsers=%v, want %v; diff=%+v", got, tc.drops, diff)
				}
			})
		}
	}
}
