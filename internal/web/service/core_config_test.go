package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type configProbe struct{ statusBase }

func (p configProbe) ValidateInstance(_ core.Kind, settings string) error {
	if settings == `{"port":0}` {
		return errors.New("probe: invalid listener port")
	}
	return nil
}

func TestCoreConfigUsesSelectedAdapterAndNeverAnotherTemplate(t *testing.T) {
	initTestDB(t)
	withWiredCore(t, configProbe{statusBase{id: "probe", kinds: []core.Kind{"probe-kind"}}})
	for _, row := range []model.Inbound{
		{Protocol: "probe-kind", Tag: "local", Settings: `{"port":443}`, Port: 443},
		{Protocol: "vless", Tag: "other", Settings: `{"clients":[]}`, Port: 8443},
	} {
		if err := database.GetDB().Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := &CoreConfigService{}
	view, err := s.Get("probe")
	if err != nil {
		t.Fatal(err)
	}
	if view.TemplateEditable || view.Template != "" || !view.InstanceValidation || len(view.Instances) != 1 || view.Instances[0].Tag != "local" {
		t.Fatalf("config = %+v", view)
	}
	if err := s.Validate(context.Background(), "probe", "probe-kind", `{"port":0}`); err == nil || err.Error() != "probe: invalid listener port" {
		t.Fatalf("validator = %v", err)
	}
	if err := s.Validate(context.Background(), "probe", "vless", `{}`); err == nil || err.Error() != "protocol does not belong to the selected core" {
		t.Fatalf("wrong core = %v", err)
	}
	if err := s.Validate(context.Background(), "probe", "probe-kind", `null`); err == nil || err.Error() != "configuration must be a JSON object" {
		t.Fatalf("null config = %v", err)
	}
}

func TestCoreConfigMissingBinaryCannotPassTest(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	err := testXrayTemplate(context.Background(), `{"outbounds":[]}`)
	if err == nil {
		t.Fatal("a missing core binary was reported as tested")
	}
}
