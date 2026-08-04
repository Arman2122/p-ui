package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// recordingCore stands in for the xray core and keeps whatever the runtime
// applied, which is the only way to see the bytes the hot path would send.
type recordingCore struct {
	kinds   []core.Kind
	applied []core.Instance
}

func (c *recordingCore) Describe() core.Descriptor {
	return core.Descriptor{ID: "recorder", TitleKey: "core.recorder"}
}
func (c *recordingCore) Kinds() []core.Kind              { return c.kinds }
func (c *recordingCore) Preflight(context.Context) error { return nil }

func (c *recordingCore) ApplyInstance(_ context.Context, inst core.Instance) error {
	c.applied = append(c.applied, inst)
	return nil
}
func (c *recordingCore) DropInstance(context.Context, core.Instance) error { return nil }

/*
TestHotApplyRendersWhatARestartWouldApply is what the unification is for.

runtime.Local applies one inbound while Xray runs; GetXrayConfig builds what the
next restart applies. When those disagree, an inbound edited under load keeps
quota-exhausted clients, loses its fallbacks and carries panel-only fields — and
because InboundConfig.Equals compares bytes, the running inbound stops matching
the generator, so every restart check afterwards reads a pending change.

Driven through runtime.Local rather than calling RenderInbound directly, so a
core that mangles what it is handed fails here too. It builds its own LocalDeps,
so it cannot see web.go dropping the wiring — TestLocalRuntimeIsWiredToRender
guards that.
*/
func TestHotApplyRendersWhatARestartWouldApply(t *testing.T) {
	initTestDB(t)
	seedGoldenFixture(t)

	svc := &XrayService{}
	cfg, err := svc.GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}
	onRestart := make(map[string]*core.InboundConfig, len(cfg.InboundConfigs))
	for i := range cfg.InboundConfigs {
		onRestart[cfg.InboundConfigs[i].Tag] = &cfg.InboundConfigs[i]
	}

	var rows []*model.Inbound
	if err := database.GetDB().Where("enable = ?", true).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read inbounds: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no enabled inbounds seeded; this test would certify nothing")
	}

	checked := 0
	for _, row := range rows {
		want, servedLocally := onRestart[row.Tag]
		if !servedLocally {
			continue
		}
		t.Run(row.Tag, func(t *testing.T) {
			recorder := &recordingCore{kinds: []core.Kind{core.Kind(row.Protocol)}}
			registry := core.NewRegistry()
			if err := registry.Register(recorder); err != nil {
				t.Fatalf("register recorder: %v", err)
			}
			local := runtime.NewLocal(runtime.LocalDeps{
				Cores:         registry,
				RenderInbound: svc.RenderInbound,
			})
			if err := local.AddInbound(t.Context(), row); err != nil {
				t.Fatalf("AddInbound: %v", err)
			}
			if len(recorder.applied) != 1 {
				t.Fatalf("the core saw %d instances, want 1", len(recorder.applied))
			}
			got := recorder.applied[0]

			for _, section := range []struct {
				name string
				got  string
				want string
			}{
				{"settings", got.Settings, string(want.Settings)},
				{"streamSettings", got.StreamSettings, string(want.StreamSettings)},
				{"sniffing", got.Sniffing, string(want.Sniffing)},
			} {
				if section.got != section.want {
					t.Errorf("%s differs between a hot apply and a restart, so the running inbound stops matching the generator\n--- hot ---\n%s\n--- restart ---\n%s",
						section.name, section.got, section.want)
				}
			}
		})
		checked++
	}
	if checked == 0 {
		t.Fatal("compared no inbounds; the tags no longer line up and this test is vacuous")
	}
}

/*
TestHotApplyWithoutARendererKeepsTheStoredSections pins the fallback.

A nil RenderInbound is what every test that builds a Local by hand gets, and it
must stay the old behaviour rather than an empty config — an inbound applied
with empty settings would drop every client on it.
*/
func TestHotApplyWithoutARendererKeepsTheStoredSections(t *testing.T) {
	initTestDB(t)
	seedGoldenFixture(t)

	var row model.Inbound
	if err := database.GetDB().Where("tag = ?", "golden-vless").First(&row).Error; err != nil {
		t.Fatalf("read inbound: %v", err)
	}

	recorder := &recordingCore{kinds: []core.Kind{core.Kind(row.Protocol)}}
	registry := core.NewRegistry()
	if err := registry.Register(recorder); err != nil {
		t.Fatalf("register recorder: %v", err)
	}
	local := runtime.NewLocal(runtime.LocalDeps{Cores: registry})
	if err := local.AddInbound(t.Context(), &row); err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	if len(recorder.applied) != 1 {
		t.Fatalf("the core saw %d instances, want 1", len(recorder.applied))
	}
	if got := recorder.applied[0].Settings; got != row.Settings {
		t.Errorf("settings = %s, want the stored blob %s", got, row.Settings)
	}
}
