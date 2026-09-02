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
	if err := loginWithACurrentCode(svc, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatalf("a correctly enrolled second factor must still work: %v", err)
	}
}

/*
loginWithACurrentCode logs in with a code generated for the window the login
lands in.

A TOTP code is valid only inside its own 30 seconds, and CheckUser hashes the
password before it ever looks at the code. On a loaded runner under -race that
hash can outlast the remainder of a window the code was generated near the end
of, and the login is then refused for the clock rather than for the code. This
failed on CI exactly that way.

Retried from the start of a fresh window rather than slept into one, so the
common path costs nothing: a refusal that is real fails both times.
*/
func loginWithACurrentCode(svc *UserService, secret string) error {
	_, err := svc.CheckUser("admin", "correct-horse", gotp.NewDefaultTOTP(secret).Now())
	if err == nil {
		return nil
	}
	const window = 30
	time.Sleep(time.Duration(window-time.Now().Unix()%window)*time.Second + 200*time.Millisecond)
	_, err = svc.CheckUser("admin", "correct-horse", gotp.NewDefaultTOTP(secret).Now())
	return err
}

/*
A two-factor code pasted with surrounding whitespace is accepted at login.

The settings-side verifier trimmed the submitted code and the login path
compared it raw, so the same pasted code passed one and was refused by the
other. Ledger L14, decided 2026-09-02: both paths trim. Retried from the start
of a fresh window for the same reason loginWithACurrentCode is.
*/
func TestLoginAcceptsAPaddedTwoFactorCode(t *testing.T) {
	initTestDB(t)
	seedPanelUser(t, "admin", "correct-horse")
	setSettings(t, map[string]string{"twoFactorEnable": "true", "twoFactorToken": "JBSWY3DPEHPK3PXP"})
	svc := &UserService{}
	padded := func() string { return " " + gotp.NewDefaultTOTP("JBSWY3DPEHPK3PXP").Now() + "\n" }

	_, err := svc.CheckUser("admin", "correct-horse", padded())
	if err != nil {
		const window = 30
		time.Sleep(time.Duration(window-time.Now().Unix()%window)*time.Second + 200*time.Millisecond)
		_, err = svc.CheckUser("admin", "correct-horse", padded())
	}
	if err != nil {
		t.Fatalf("a current code with surrounding whitespace was refused: %v", err)
	}
	// Seven digits can never be a TOTP code, padded or not.
	if _, err := svc.CheckUser("admin", "correct-horse", " 1234567 "); err == nil {
		t.Fatal("a padded but wrong code was accepted: trimming must not weaken the compare")
	}
}

/*
LDAP must not authorise a panel administrator without its own opt-in.

ldapEnable is the CLIENT-SYNC switch. AuthenticateUser proves a directory
account exists and knows its own password — nothing about administering this
panel — so with ldapEnable on, every account matching ldapUserFilter (default
`(objectClass=person)`) could sign in here as admin. F375.

The assertion is that the directory is never CONTACTED when no admin filter is
set. An earlier version of this test asserted on elapsed time instead and passed
with the guard removed, because a refused connection returns instantly: the
listener below is what makes the difference observable.
*/
func TestLdapIsNotEvenContactedWithoutAnAdminFilter(t *testing.T) {
	initTestDB(t)
	seedPanelUser(t, "admin", "correct-horse")

	host, port, dialed := recordingListener(t)
	setSettings(t, map[string]string{
		"ldapEnable":      "true",
		"ldapHost":        host,
		"ldapPort":        strconv.Itoa(port),
		"ldapAdminFilter": "",
	})

	if _, err := (&UserService{}).CheckUser("admin", "wrong-password", ""); err == nil {
		t.Fatal("a wrong panel password fell through to LDAP and was accepted")
	}
	if dialed() {
		t.Fatal("the panel contacted the directory with no admin filter set: ldapEnable alone must not authorise a panel administrator")
	}

	// With a filter set, the directory IS consulted — proving the gate is the
	// filter and not something else refusing earlier.
	setSettings(t, map[string]string{"ldapAdminFilter": "(memberOf=cn=panel-admins,dc=example,dc=test)"})
	_, _ = (&UserService{}).CheckUser("admin", "wrong-password", "")
	if !dialed() {
		t.Fatal("with an admin filter set the directory must be consulted; nothing reached the listener")
	}

	// The local password is untouched by all of this, which is why failing closed
	// locks nobody out.
	if _, err := (&UserService{}).CheckUser("admin", "correct-horse", ""); err != nil {
		t.Fatalf("the panel's own password must still work: %v", err)
	}
}

// recordingListener accepts and immediately closes, reporting whether anything
// ever connected. That single bit is the whole assertion above.
func recordingListener(t *testing.T) (host string, port int, dialed func() bool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var hits atomic.Bool
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			hits.Store(true)
			_ = conn.Close()
		}
	}()

	addr := listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, hits.Load
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
