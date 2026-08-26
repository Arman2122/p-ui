package service

import (
	"os"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/web/entity"
)

/*
2FA cannot be switched on without a secret — checked on the EFFECTIVE token.

This is the one place in F208's fix where reading the wrong token source
silently defeats it. preserveRedactedSecrets carries a stored secret forward
when the submission is blank, because a blank submission means "keep what is
stored". So the refusal must evaluate enable && effective-token-blank:

  - read the SUBMITTED token and an operator re-saving settings with a redacted
    (blank) field trips a false refusal while their real secret sits in the
    database
  - read it before preservation runs and the blank-forever state persists,
    which is the state the login path now refuses — an operator locked out by
    the very guard meant to protect them

The three cases below are exactly that boundary.
*/
func TestTwoFactorCannotBeEnabledWithoutASecret(t *testing.T) {
	cases := []struct {
		name    string
		enable  bool
		token   string
		refused bool
		because string
	}{
		{
			name:    "enabled with nothing stored and nothing submitted",
			enable:  true,
			token:   "",
			refused: true,
			because: "this is the state that makes the second factor a public constant",
		},
		{
			name:    "enabled with a freshly typed secret",
			enable:  true,
			token:   "JBSWY3DPEHPK3PXP",
			refused: false,
			because: "a real enrolment must not be refused",
		},
		{
			name:    "enabled with a secret preserved from storage",
			enable:  true,
			token:   "JBSWY3DPEHPK3PXP", // what preserveRedactedSecrets carried forward
			refused: false,
			because: "re-saving settings with the field redacted must not trip the guard",
		},
		{
			name:    "disabled with no secret is the ordinary off state",
			enable:  false,
			token:   "",
			refused: false,
			because: "2FA off needs no secret",
		},
		{
			name:    "whitespace is not a secret",
			enable:  true,
			token:   "   ",
			refused: true,
			because: "gotp trims nothing; a blank-looking token is a blank key",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := refuseTwoFactorWithoutASecret(&entity.AllSetting{
				TwoFactorEnable: c.enable,
				TwoFactorToken:  c.token,
			})
			if c.refused && err == nil {
				t.Fatalf("expected a refusal: %s", c.because)
			}
			if !c.refused && err != nil {
				t.Fatalf("expected acceptance (%s), got: %v", c.because, err)
			}
			if c.refused && !strings.Contains(err.Error(), "secret") {
				t.Fatalf("the refusal must tell the operator what is missing, got: %v", err)
			}
		})
	}
}

/*
And the guard runs AFTER preservation, not before.

UpdateAllSetting's ordering is the whole correctness argument above: a blank
submission over a real stored secret has to reach the guard already filled in.
Asserted on the source because the ordering is invisible at runtime — both
orders "work" until the redacted-resave case, which is the one an operator hits
every time they change any other setting.
*/
func TestTheTwoFactorGuardRunsAfterSecretsArePreserved(t *testing.T) {
	body := readServiceSource(t, "setting.go")

	preserve := strings.Index(body, "preserveRedactedSecrets(allSetting, clears)")
	guard := strings.Index(body, "refuseTwoFactorWithoutASecret(allSetting)")
	if preserve < 0 || guard < 0 {
		t.Fatal("UpdateAllSetting no longer preserves secrets or no longer guards 2FA; this test is now vacuous")
	}
	if guard < preserve {
		t.Fatal("the 2FA guard runs before preserveRedactedSecrets: a redacted resave would then be refused even though a real secret is stored")
	}
}

// readServiceSource reads one file of this package as text. The ordering asserted
// above is a property of the source, not of any value at runtime.
func readServiceSource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}
