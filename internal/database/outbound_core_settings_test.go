package database

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/xtls/xray-core/infra/conf"
	"google.golang.org/protobuf/proto"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestDNSOutboundNormalizationPreservesCorePolicy(t *testing.T) {
	for _, settings := range []string{
		`{"nonIPQuery":"reject","blockTypes":[0,28]}`,
		`{"nonIPQuery":"drop","blockTypes":[0]}`,
		`{"nonIPQuery":"skip","blockTypes":[1,16]}`,
		`{"blockTypes":[]}`,
	} {
		t.Run(settings, func(t *testing.T) {
			var before conf.DNSOutboundConfig
			if err := json.Unmarshal([]byte(settings), &before); err != nil {
				t.Fatal(err)
			}
			want, err := before.Build()
			if err != nil {
				t.Fatal(err)
			}
			raw := `{"outbounds":[{"protocol":"DNS","settings":` + settings + `}]}`
			updated, changed, err := NormalizeXrayOutboundSettings(raw)
			if err != nil || !changed {
				t.Fatalf("normalize = (%q, %v, %v), want migrated policy", updated, changed, err)
			}
			var cfg struct {
				Outbounds []struct {
					Settings conf.DNSOutboundConfig `json:"settings"`
				} `json:"outbounds"`
			}
			if err := json.Unmarshal([]byte(updated), &cfg); err != nil {
				t.Fatal(err)
			}
			after := cfg.Outbounds[0].Settings
			if after.NonIPQuery != nil || after.BlockTypes != nil {
				t.Fatalf("legacy keys remain: %s", updated)
			}
			got, err := after.Build()
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(got, want) {
				t.Fatalf("normalized DNS policy = %v, want original %v", got, want)
			}
			if again, changed, err := NormalizeXrayOutboundSettings(updated); err != nil || changed || again != updated {
				t.Fatalf("second normalization = (%q, %v, %v), want unchanged", again, changed, err)
			}
		})
	}
}

func TestOutboundCoreSettingsUpgrade(t *testing.T) {
	initTestDB(t)
	template := `{"outbounds":[
		{"protocol":"FrEeDoM","targetStrategy":"UseIPv6","settings":{"domainStrategy":"UseIP","finalRules":[{"action":"allow"}]},"streamSettings":{"sockopt":{"domainStrategy":"UseIPv4","mark":7,"addressPortStrategy":"srvportonly"}}},
		{"protocol":"freedom","settings":{"targetStrategy":"AsIs","domainStrategy":"UseIP","finalRules":[{"action":"allow"}]},"streamSettings":{"sockopt":{"domainStrategy":"UseIPv4"}}},
		{"protocol":"DNS","settings":{"nonIPQuery":"skip","blockTypes":[0,28],"userLevel":8}},
		{"protocol":"dns","settings":{"nonIPQuery":"drop","blockTypes":[1],"rules":[{"action":"return","qType":0,"rCode":3}]}},
		{"protocol":"Freedom","settings":{"finalRules":[{"action":"allow","ip":["192.168.0.0/16"]}]}}
	]}`
	if err := db.Create(&model.Setting{Key: "xrayTemplateConfig", Value: template}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("seeder_name = ?", "OutboundCoreSettings").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runSeeders(false); err != nil {
		t.Fatal(err)
	}
	var setting model.Setting
	if err := db.Where("key = ?", "xrayTemplateConfig").First(&setting).Error; err != nil {
		t.Fatal(err)
	}
	want := `{"outbounds":[
		{"protocol":"FrEeDoM","settings":{"finalRules":[{"action":"block","ip":["geoip:private"]},{"action":"allow"}]},"streamSettings":{"sockopt":{"domainStrategy":"UseIPv6","mark":7}}},
		{"protocol":"freedom","settings":{"finalRules":[{"action":"allow"}]},"streamSettings":{"sockopt":{"domainStrategy":"UseIPv4"}}},
		{"protocol":"DNS","settings":{"userLevel":8,"rules":[{"action":"drop","qType":"0,28"},{"action":"hijack","qType":"1,28"},{"action":"direct"}]}},
		{"protocol":"dns","settings":{"rules":[{"action":"return","qType":"0","rCode":3}]}},
		{"protocol":"Freedom","settings":{"finalRules":[{"action":"allow","ip":["192.168.0.0/16"]}]}}
	]}`
	var gotJSON, wantJSON any
	if err := json.Unmarshal([]byte(setting.Value), &gotJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("migrated template:\n%s\nwant:\n%s", setting.Value, want)
	}
	var count int64
	if err := db.Model(&model.HistoryOfSeeders{}).Where("seeder_name = ?", "OutboundCoreSettings").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration history count = %d, want 1", count)
	}
	if err := runSeeders(false); err != nil {
		t.Fatal(err)
	}
	var again model.Setting
	if err := db.Where("key = ?", "xrayTemplateConfig").First(&again).Error; err != nil {
		t.Fatal(err)
	}
	if again.Value != setting.Value {
		t.Fatalf("second startup changed migrated settings:\n%s", again.Value)
	}
}
