package panel

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPanelUpdaterForwardsAmbientProxyToSystemd(t *testing.T) {
	initTestDB(t)
	resetUpdateSlot(t)
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("PUI_UPDATE_CAPTURE", filepath.Join(dir, "args"))
	t.Setenv("PATH", dir)
	for name, script := range map[string]string{
		"bash":        "#!/bin/sh\nexit 91\n",
		"systemd-run": "#!/bin/sh\nprintf '%s\\000' \"$@\" > \"$PUI_UPDATE_CAPTURE\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("#!/bin/sh\nexit 92\n"))
	}))
	t.Cleanup(server.Close)
	previous := panelReleaseBaseURL
	panelReleaseBaseURL = server.URL + "/"
	t.Cleanup(func() { panelReleaseBaseURL = previous })

	keys := []string{"https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY", "http_proxy", "HTTP_PROXY", "no_proxy", "NO_PROXY"}
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"separate proxy families", map[string]string{"https_proxy": "http://127.0.0.1:8181", "ALL_PROXY": "socks5://127.0.0.1:9191", "no_proxy": "localhost,127.0.0.1"}},
		{"HTTP only is not promoted", map[string]string{"http_proxy": "http://127.0.0.1:8181", "NO_PROXY": "localhost,127.0.0.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			releaseUpdateSlot()
			for _, key := range keys {
				t.Setenv(key, tc.env[key])
			}
			if _, err := (&PanelService{}).startUpdate(true); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(os.Getenv("PUI_UPDATE_CAPTURE"))
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(string(raw), "\x00")
			got := map[string]string{}
			for i := 0; i+1 < len(args); i++ {
				if args[i] != "--setenv" {
					continue
				}
				key, value, _ := strings.Cut(args[i+1], "=")
				for _, proxyKey := range keys {
					if key == proxyKey {
						got[key] = value
					}
				}
			}
			if !reflect.DeepEqual(got, tc.env) {
				t.Fatalf("systemd child proxy environment = %v, want %v", got, tc.env)
			}
		})
	}
}
