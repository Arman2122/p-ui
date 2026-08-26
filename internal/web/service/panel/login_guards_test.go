package panel

import (
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xlzd/gotp"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
)

/*
An empty TOTP secret still produces a computable code — this is the measurement
the fix rests on.

gotp base32-decodes "" to an empty key without error and HMACs the time counter
under it, so the "expected" code is a public function of the clock. If gotp ever
starts erroring on an empty secret, this test says so, and whoever sees it
should learn the guard became belt-and-braces here rather than by deleting it.
F208.
*/
func TestAnEmptySecretStillProducesAComputableCode(t *testing.T) {
	code := gotp.NewDefaultTOTP("").Now()
	if len(code) != 6 {
		t.Fatalf("expected a six-digit code from an empty secret, got %q", code)
	}
	if !gotp.NewDefaultTOTP("").Verify(code, time.Now().Unix()) {
		t.Fatal("gotp no longer accepts its own empty-secret code; F208's premise has changed and the guard's comment needs updating")
	}
}

/*
Login must refuse when 2FA is on and the secret is empty.

CheckUser compared the submitted code against gotp.NewDefaultTOTP("").Now(), so
anyone who could compute that constant satisfied the second factor. The
settings-side verifier had the guard all along; the login side did not. F208.
*/
func TestLoginRefusesTheComputableCodeWhenTheSecretIsEmpty(t *testing.T) {
	initTestDB(t)
	seedPanelUser(t, "admin", "correct-horse")
	setSettings(t, map[string]string{"twoFactorEnable": "true", "twoFactorToken": ""})

	svc := &UserService{}
	computable := gotp.NewDefaultTOTP("").Now()

	if _, err := svc.CheckUser("admin", "correct-horse", computable); err == nil {
		t.Fatal("the panel accepted a second factor anyone can compute from the clock")
	}
	// And it is the 2FA gate refusing, not the password: the same password with a
	// real secret and its real code must get in.
	setSettings(t, map[string]string{"twoFactorEnable": "true", "twoFactorToken": "JBSWY3DPEHPK3PXP"})
	if _, err := svc.CheckUser("admin", "correct-horse", gotp.NewDefaultTOTP("JBSWY3DPEHPK3PXP").Now()); err != nil {
		t.Fatalf("a correctly enrolled second factor must still work: %v", err)
	}
}

// Whitespace in a submitted code is still NOT trimmed. The settings-side
// verifier trims; this one does not. That divergence is deliberate and is ledger
// L14 — F208 adds the empty-secret guard and changes nothing else about how a
// code is compared.
func TestLoginStillComparesTheSubmittedCodeRaw(t *testing.T) {
	initTestDB(t)
	seedPanelUser(t, "admin", "correct-horse")
	setSettings(t, map[string]string{"twoFactorEnable": "true", "twoFactorToken": "JBSWY3DPEHPK3PXP"})

	padded := " " + gotp.NewDefaultTOTP("JBSWY3DPEHPK3PXP").Now() + " "
	if _, err := (&UserService{}).CheckUser("admin", "correct-horse", padded); err == nil {
		t.Fatal("the login path trimmed the submitted code: that is a behaviour change F208 does not make, and it belongs to L14")
	}
}

// seedPanelUser points the panel's single admin row at a known password.
// InitDB seeds a default user, so this UPDATES rather than inserts — a second
// row with the same username would leave CheckUser's First() reading the seeded
// one, which is how the first version of this test failed.
func seedPanelUser(t *testing.T, username, password string) {
	t.Helper()
	hashed, err := crypto.HashPasswordAsBcrypt(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	db := database.GetDB()
	existing := &model.User{}
	if err := db.Model(model.User{}).First(existing).Error; err != nil {
		t.Fatalf("read the seeded user: %v", err)
	}
	if err := db.Model(model.User{}).Where("id = ?", existing.Id).
		Updates(map[string]any{"username": username, "password": hashed}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func setSettings(t *testing.T, values map[string]string) {
	t.Helper()
	db := database.GetDB()
	for key, value := range values {
		row := &model.Setting{Key: key, Value: value}
		if err := db.Where("key = ?", key).Delete(&model.Setting{}).Error; err != nil {
			t.Fatalf("clear setting %s: %v", key, err)
		}
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
}
