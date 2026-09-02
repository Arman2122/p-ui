package sub

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The subscription server sent no browser hardening at all -- no nosniff, no
frame denial, no referrer policy, no CSP, no HSTS -- while the panel beside it
sent every one. A subscription page could be framed, and a text/plain
subscription body could be sniffed into markup. F211.
*/
func TestSubServerSendsBrowserHardeningHeaders(t *testing.T) {
	initSubDB(t)
	SetDistFS(testDistFS)

	rec := httptest.NewRecorder()
	subEngine(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/nobody", nil))

	h := rec.Result().Header
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := h.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("Content-Security-Policy = %q, want the panel's policy", csp)
	}
	// No certificate is configured, so this server does not terminate TLS and
	// must not promise a scheme a proxy in front of it owns.
	if got := h.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("Strict-Transport-Security = %q sent with no subscription certificate configured", got)
	}
}

var cspNonce = regexp.MustCompile(`'nonce-([^']+)'`)

// The built-in page injects its own inline <script>; under a nonce policy it
// has to carry the nonce the header names or the browser refuses it.
func TestSubPageScriptCarriesTheCSPNonce(t *testing.T) {
	initSubDB(t)
	SetDistFS(testDistFS)
	seedSubInbound(t, "noncesub", "nonce-vless", 45312, 0, `{"network":"tcp","security":"none"}`)

	rec := httptest.NewRecorder()
	subEngine(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/noncesub?html=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET page = %d: %s", rec.Code, rec.Body.String())
	}
	m := cspNonce.FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
	if m == nil {
		t.Fatalf("Content-Security-Policy = %q names no nonce", rec.Header().Get("Content-Security-Policy"))
	}
	if !strings.Contains(rec.Body.String(), `<script nonce="`+m[1]+`">window.PUI_BASE_PATH`) {
		t.Fatalf("the injected script does not carry nonce %q:\n%s", m[1], rec.Body.String())
	}
}

// A custom theme is HTML the operator wrote, inline scripts and all, and
// cannot be given the nonce. It gets the policy that does not depend on the
// markup, not one that silently blanks the theme.
func TestCustomThemeGetsAPolicyItCanRun(t *testing.T) {
	initSubDB(t)
	SetDistFS(testDistFS)
	seedSubInbound(t, "themesub", "theme-vless", 45313, 0, `{"network":"tcp","security":"none"}`)

	dir := t.TempDir()
	theme := `<html><body><script>document.title="custom-theme"</script>{{.sId}}</body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(theme), 0o644); err != nil {
		t.Fatalf("write theme: %v", err)
	}
	if err := database.GetDB().Create(&model.Setting{Key: "subThemeDir", Value: dir}).Error; err != nil {
		t.Fatalf("set subThemeDir: %v", err)
	}

	rec := httptest.NewRecorder()
	subEngine(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/themesub?html=1", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "custom-theme") {
		t.Fatalf("GET themed page = %d, body %q: the theme was not served", rec.Code, rec.Body.String())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "script-src") {
		t.Fatalf("Content-Security-Policy = %q: a script policy on operator-authored HTML blanks every theme with an inline script", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("Content-Security-Policy = %q, want the theme still unframeable", csp)
	}
}

func subEngine(t *testing.T) http.Handler {
	t.Helper()
	engine, err := NewServer().initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}
	return engine
}
