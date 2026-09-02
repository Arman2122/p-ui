package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/web/service/panel"
)

/*
Every API token was a permanent full admin. A token now carries a scope and
an expiry: a read token answers GET and nothing else, an expired token
answers nothing, and a token minted before scopes existed keeps full access.
Feature gap S2.
*/
func TestCheckAPIAuthHonoursTokenScopeAndExpiry(t *testing.T) {
	engine, _ := newAPIAuthTestEngine(t)
	db := database.GetDB()
	mint := func(name, scope string, expiresAt int64) string {
		t.Helper()
		plaintext := name + "-plaintext-token-value"
		row := &model.ApiToken{Name: name, Token: crypto.HashTokenSHA256(plaintext), Enabled: true, Scope: scope, ExpiresAt: expiresAt}
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("mint %s: %v", name, err)
		}
		return plaintext
	}
	call := func(method, path, token string) int {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec.Code
	}

	reader := mint("reader", model.ApiTokenScopeRead, 0)
	if got := call(http.MethodGet, "/panel/api/ping", reader); got != http.StatusOK {
		t.Fatalf("read token GET = %d, want 200", got)
	}
	if got := call(http.MethodPost, "/panel/api/write", reader); got != http.StatusForbidden {
		t.Fatalf("read token POST = %d, want 403", got)
	}
	admin := mint("admin", model.ApiTokenScopeAdmin, 0)
	if got := call(http.MethodPost, "/panel/api/write", admin); got != http.StatusOK {
		t.Fatalf("admin token POST = %d, want 200", got)
	}
	legacy := mint("legacy", "", time.Now().Unix()+3600)
	if got := call(http.MethodPost, "/panel/api/write", legacy); got != http.StatusOK {
		t.Fatalf("a token with no scope must keep full access, POST = %d", got)
	}
	expired := mint("expired", model.ApiTokenScopeAdmin, time.Now().Unix()-60)
	if got := call(http.MethodGet, "/panel/api/ping", expired); got != http.StatusUnauthorized {
		t.Fatalf("expired token GET = %d, want 401", got)
	}

	svc := &panel.ApiTokenService{}
	if _, err := svc.Create("past", model.ApiTokenScopeAdmin, time.Now().Unix()-1); err == nil {
		t.Fatal("a token expiring in the past was minted")
	}
	if _, err := svc.Create("odd", "root", 0); err == nil {
		t.Fatal("a token with an unknown scope was minted")
	}
}
