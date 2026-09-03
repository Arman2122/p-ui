package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// mintingCore fills a server key an operator cannot type, and keeps one it finds.
type mintingCore struct{ stubCore }

func (c mintingCore) MintInstance(kind core.Kind, settings, previous string) (string, error) {
	if kind != c.kinds[0] {
		return settings, nil
	}
	doc := map[string]any{}
	if strings.TrimSpace(settings) != "" {
		if err := json.Unmarshal([]byte(settings), &doc); err != nil {
			return "", err
		}
	}
	if key, _ := doc["serverKey"].(string); key == "" {
		old := map[string]any{}
		_ = json.Unmarshal([]byte(previous), &old)
		if was, _ := old["serverKey"].(string); was != "" {
			doc["serverKey"] = was
		} else {
			doc["serverKey"] = "minted-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}
	}
	out, err := json.Marshal(doc)
	return string(out), err
}

/*
An inbound's own secrets (a server certificate, a tls-crypt-v2 server key)
ride rows, so a restore is complete, and nobody types them: the core mints
what is missing when the inbound is added and keeps it on every later save,
because clients pin it. A core with no minter leaves settings alone.
*/
func TestInboundSecretsAreMintedOnAddAndKeptOnUpdate(t *testing.T) {
	initTestDB(t)
	reg, err := cores.Default(cores.Deps{})
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	stub := mintingCore{stubCore{descriptor: core.Descriptor{ID: "mintcore", TitleKey: "cores.mint.title"}, kinds: []core.Kind{"mintkind"}}}
	if err := reg.Register(stub); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	cores.Use(reg)
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{Cores: reg}))

	svc := &InboundService{}
	added, _, _, err := svc.AddInbound(&model.Inbound{UserId: 1, Protocol: model.Protocol("mintkind"), Tag: "mint-1", Port: 24501, Settings: `{"clients":[]}`, StreamSettings: `{}`})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	minted := serverKeyOf(t, added.Settings)
	if !strings.HasPrefix(minted, "minted-") {
		t.Fatalf("add did not mint the server key: %s", added.Settings)
	}

	edited := *added
	edited.Remark = "renamed"
	updated, _, _, err := svc.UpdateInbound(&edited)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if serverKeyOf(t, updated.Settings) != minted {
		t.Fatalf("update lost the minted key: %s", updated.Settings)
	}
	// A save that omits the key (an API client sending only what it knows)
	// keeps what the row held rather than minting a new one.
	stripped := *updated
	stripped.Settings = `{"clients":[]}`
	kept, _, _, err := svc.UpdateInbound(&stripped)
	if err != nil {
		t.Fatalf("update without the key: %v", err)
	}
	if serverKeyOf(t, kept.Settings) != minted {
		t.Fatalf("a save without the key rotated it: %s -> %s", minted, serverKeyOf(t, kept.Settings))
	}

	plain, _, _, err := svc.AddInbound(&model.Inbound{UserId: 1, Protocol: model.VLESS, Tag: "plain-1", Port: 24502, Settings: `{"clients":[],"decryption":"none"}`, StreamSettings: `{"network":"tcp"}`})
	if err != nil {
		t.Fatalf("add vless: %v", err)
	}
	if serverKeyOf(t, plain.Settings) != "" {
		t.Fatalf("a kind with no minter was touched: %s", plain.Settings)
	}
	_ = context.Background()
}

func serverKeyOf(t *testing.T, settings string) string {
	t.Helper()
	var doc struct {
		ServerKey string `json:"serverKey"`
	}
	if err := json.Unmarshal([]byte(settings), &doc); err != nil {
		t.Fatalf("settings do not parse: %v", err)
	}
	return doc.ServerKey
}
