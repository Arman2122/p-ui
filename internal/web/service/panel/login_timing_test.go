package panel

import (
	"testing"
	"time"
)

/*
A login failure must cost the same whether or not the username exists.

CheckUser returned on the record-not-found branch before ever reaching bcrypt,
so an unknown username answered in the time of one indexed SELECT and a known
one in the time of a full bcrypt verify -- a measurable oracle for which
usernames are real. F212.

Asserted as a ratio rather than an absolute, so a slow CI box and a fast laptop
agree: without the fix the unknown-user path is ~1% of the known-user path,
with it the two are the same bcrypt. Minimum of three runs each, so one GC
pause cannot flip the verdict.
*/
func TestUnknownUsernameCostsAsMuchAsAWrongPassword(t *testing.T) {
	initTestDB(t)
	seedPanelUser(t, "admin", "correct-horse")
	svc := &UserService{}

	fastest := func(username string) time.Duration {
		best := time.Hour
		for range 3 {
			start := time.Now()
			if _, err := svc.CheckUser(username, "wrong-password", ""); err == nil {
				t.Fatalf("CheckUser(%q, wrong password) succeeded", username)
			}
			if d := time.Since(start); d < best {
				best = d
			}
		}
		return best
	}

	known := fastest("admin")
	unknown := fastest("nobody-by-this-name")
	if unknown*2 < known {
		t.Fatalf("unknown user answered in %v, a known user in %v: the not-found branch skips bcrypt and tells the caller which usernames exist", unknown, known)
	}
}
