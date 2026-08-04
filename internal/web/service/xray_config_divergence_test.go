package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The two renderers disagree, and this is the executable version of that claim.

GetXrayConfig is what a restart applies. GenXrayInboundConfig is what the
per-inbound hot path applies — runtime's instanceOf feeds the xray core exactly
these bytes, cross-checked by TestInstanceCarriesWhatTheConfigGeneratorWouldEmit.
So an inbound edited while Xray runs gets one config and the next restart gives
it another.

The drift is not only cosmetic. InboundConfig.Equals compares raw bytes, so once
an inbound has been hot-applied the running config no longer matches what the
generator produces, and the restart check reads that as a pending change.

Pinned the way knownProtocolDivergence pinned the protocol lists: *resolving*
the divergence fails this test too, so the pin cannot outlive the fix. When the
renderers are unified, delete it — testdata/xray_inbounds.golden is what
protects the behaviour afterwards.
*/
func TestTheTwoInboundRenderersStillDisagree(t *testing.T) {
	initTestDB(t)
	seedGoldenFixture(t)

	cfg, err := (&XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}

	const tag = "golden-vless"
	var full *core.InboundConfig
	for i := range cfg.InboundConfigs {
		if cfg.InboundConfigs[i].Tag == tag {
			full = &cfg.InboundConfigs[i]
		}
	}
	if full == nil {
		t.Fatalf("%s is not in the generated config", tag)
	}

	// A fresh read: GetXrayConfig rewrites the rows it renders in place.
	var row model.Inbound
	if err := database.GetDB().Where("tag = ?", tag).First(&row).Error; err != nil {
		t.Fatalf("read %s: %v", tag, err)
	}
	hot := row.GenXrayInboundConfig()

	for _, section := range []struct {
		name string
		full string
		hot  string
		why  string
	}{
		{
			name: "settings",
			full: string(full.Settings), hot: string(hot.Settings),
			why: "the full path rebuilds clients from the clients table and attaches fallbacks; the hot path keeps whatever the stored blob says",
		},
		{
			name: "streamSettings",
			full: string(full.StreamSettings), hot: string(hot.StreamSettings),
			why: "the full path drops externalProxy, realitySettings.settings and a finalmask that crashes Xray under REALITY; the hot path keeps all three",
		},
	} {
		if section.full == section.hot {
			t.Errorf("the two renderers now agree on %s — if that is the unification, delete this test and let the golden fixture guard it (%s)", section.name, section.why)
		}
	}
}
