package panel

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestRecreateTokenReplacesOnlyItsNamedCredential(t *testing.T) {
	initTestDB(t)
	svc := &ApiTokenService{}
	other, err := svc.Create("unrelated", model.ApiTokenScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.RecreateByName("cli-fallback")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.RecreateByName("cli-fallback")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.Match(first.Token); ok {
		t.Fatal("previous CLI token still authorizes requests")
	}
	if scope, ok := svc.Match(second.Token); !ok || scope != model.ApiTokenScopeAdmin {
		t.Fatalf("replacement matches=%v scope=%q", ok, scope)
	}
	if scope, ok := svc.Match(other.Token); !ok || scope != model.ApiTokenScopeRead {
		t.Fatalf("unrelated token matches=%v scope=%q", ok, scope)
	}
	var count int64
	if err := database.GetDB().Model(&model.ApiToken{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("token rows=%d, want 2", count)
	}
	if err := database.GetDB().Exec("ALTER TABLE api_tokens ADD CONSTRAINT deny_cli_replacement CHECK (name <> 'cli-fallback') NOT VALID").Error; err != nil {
		t.Fatal(err)
	}
	_, err = svc.RecreateByName("cli-fallback")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "deny_cli_replacement" {
		t.Fatalf("replacement failure = %v, want deny_cli_replacement constraint", err)
	}
	if _, ok := svc.Match(second.Token); !ok {
		t.Fatal("failed replacement revoked the working credential")
	}
}
