package ssh

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/core"
)

func TestSSHInstalledCapability(t *testing.T) {
	bound := core.Bind(New(Deps{}))
	if bound.Version == nil {
		t.Fatal("embedded SSH must report its compiled version")
	}
	if bound.Versions != nil {
		t.Fatal("embedded SSH must not offer independent downloads or installation")
	}
	version, err := bound.Version.Installed(context.Background())
	if err != nil {
		t.Fatalf("Installed: %v", err)
	}
	prefix := "Penhoon SSH " + config.GetPanelVersion() + " (golang.org/x/crypto "
	if !strings.HasPrefix(version, prefix) || !strings.HasSuffix(version, ")") {
		t.Fatalf("Installed = %q, want compiled panel and SSH module identity", version)
	}
	if strings.Contains(version, "OpenSSH") || strings.Contains(version, serverVersion) {
		t.Fatalf("Installed = %q, must not report the compatibility banner", version)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := bound.Version.Installed(ctx); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Installed = %q, %v; want empty, context.Canceled", got, err)
	}
}

func TestSSHInstalledModuleMetadata(t *testing.T) {
	tests := []struct {
		name   string
		panel  string
		info   *debug.BuildInfo
		engine string
	}{
		{name: "compiled dependency", panel: "1.5.3", info: &debug.BuildInfo{Deps: []*debug.Module{
			{Path: "example.org/unrelated", Version: "v9.6.0"},
			{Path: "golang.org/x/crypto", Version: "v0.56.0"},
		}}, engine: "golang.org/x/crypto v0.56.0"},
		{name: "development panel and pseudo-version", panel: "dev+1234abcd", info: &debug.BuildInfo{Deps: []*debug.Module{
			{Path: "golang.org/x/crypto", Version: "v0.56.1-0.20260912000000-123456789abc"},
		}}, engine: "golang.org/x/crypto v0.56.1-0.20260912000000-123456789abc"},
		{name: "versioned replacement", panel: "1.5.3", info: &debug.BuildInfo{Deps: []*debug.Module{
			{Path: "golang.org/x/crypto", Version: "v0.56.0", Replace: &debug.Module{Path: "example.org/crypto-fork", Version: "v1.2.3"}},
		}}, engine: "golang.org/x/crypto replaced by example.org/crypto-fork v1.2.3"},
		{name: "same-module replacement", panel: "1.5.3", info: &debug.BuildInfo{Deps: []*debug.Module{
			{Path: "golang.org/x/crypto", Version: "v0.56.0", Replace: &debug.Module{Path: "golang.org/x/crypto", Version: "v0.57.0"}},
		}}, engine: "golang.org/x/crypto replaced by golang.org/x/crypto v0.57.0"},
		{name: "local replacement does not inherit pinned version", panel: "1.5.3", info: &debug.BuildInfo{Deps: []*debug.Module{
			{Path: "golang.org/x/crypto", Version: "v0.56.0", Replace: &debug.Module{Path: "/builder/private/crypto"}},
		}}, engine: "golang.org/x/crypto local replacement; version unknown"},
		{name: "unversioned dependency", panel: "1.5.3", info: &debug.BuildInfo{Deps: []*debug.Module{
			{Path: "golang.org/x/crypto", Version: "(devel)"},
		}}, engine: "golang.org/x/crypto version unknown"},
		{name: "missing dependency metadata", panel: "1.5.3", info: &debug.BuildInfo{Deps: []*debug.Module{nil}}, engine: "golang.org/x/crypto version unknown"},
		{name: "no build information", panel: "1.5.3", engine: "golang.org/x/crypto version unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := "Penhoon SSH " + tt.panel + " (" + tt.engine + ")"
			if got := embeddedSSHVersion(tt.panel, tt.info); got != want {
				t.Fatalf("embeddedSSHVersion = %q, want %q", got, want)
			}
		})
	}
}
