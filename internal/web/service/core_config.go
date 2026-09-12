package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

type CoreConfigInstanceView struct {
	ID             int    `json:"id" example:"4"`
	Tag            string `json:"tag" example:"inbound-443"`
	Remark         string `json:"remark" example:"Main listener"`
	Kind           string `json:"kind" example:"tuic"`
	Enabled        bool   `json:"enabled" example:"true"`
	Listen         string `json:"listen" example:"0.0.0.0"`
	Port           int    `json:"port" example:"443"`
	Settings       string `json:"settings" example:"{}"`
	StreamSettings string `json:"streamSettings" example:"{}"`
	Sniffing       string `json:"sniffing" example:"{}"`
}

type CoreConfigView struct {
	ID                 string                   `json:"id" example:"singbox"`
	TitleKey           string                   `json:"titleKey" example:"cores.singbox.title"`
	Template           string                   `json:"template" example:""`
	TemplateEditable   bool                     `json:"templateEditable" example:"false"`
	InstanceValidation bool                     `json:"instanceValidation" example:"true"`
	Instances          []CoreConfigInstanceView `json:"instances"`
}

type CoreConfigService struct{}

func (s *CoreConfigService) Get(id string) (CoreConfigView, error) {
	bound, err := boundCore(id)
	if err != nil {
		return CoreConfigView{}, err
	}
	view := CoreConfigView{ID: id, TitleKey: bound.Core.Describe().TitleKey, TemplateEditable: id == "xray", InstanceValidation: bound.Validate != nil, Instances: []CoreConfigInstanceView{}}
	if view.TemplateEditable {
		view.Template, err = (&SettingService{}).GetXrayConfigTemplate()
		if err != nil {
			return view, err
		}
	}
	var rows []model.Inbound
	if err := database.GetDB().Where("node_id IS NULL AND protocol IN ?", bound.Core.Kinds()).Order("id ASC").Find(&rows).Error; err != nil {
		return view, err
	}
	for _, row := range rows {
		view.Instances = append(view.Instances, CoreConfigInstanceView{ID: row.Id, Tag: row.Tag, Remark: row.Remark, Kind: string(row.Protocol), Enabled: row.Enable, Listen: row.Listen, Port: row.Port, Settings: row.Settings, StreamSettings: row.StreamSettings, Sniffing: row.Sniffing})
	}
	return view, nil
}

func (s *CoreConfigService) Validate(ctx context.Context, id, kind, document string) error {
	bound, err := boundCore(id)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(document), &object); err != nil {
		return err
	}
	if object == nil {
		return errors.New("configuration must be a JSON object")
	}
	if id == "xray" && kind == "" {
		return testXrayTemplate(ctx, document)
	}
	found := false
	for _, supported := range bound.Core.Kinds() {
		if supported == core.Kind(kind) {
			found = true
			break
		}
	}
	if !found {
		return errors.New("protocol does not belong to the selected core")
	}
	if bound.Validate == nil {
		return errors.New("this adapter does not support independent configuration validation; validation occurs when applying the inbound")
	}
	return bound.Validate.ValidateInstance(core.Kind(kind), document)
}

func testXrayTemplate(ctx context.Context, document string) error {
	if err := (&XraySettingService{}).CheckXrayConfig(document); err != nil {
		return err
	}
	var cfg xray.Config
	if err := json.Unmarshal([]byte(document), &cfg); err != nil {
		return err
	}
	cfg.InboundConfigs = nil
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(xray.GetConfigPath()), ".config-test-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, xray.GetBinaryPath(), "-test", "-c", file.Name()).CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("core configuration test: %w", ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("core configuration test: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
