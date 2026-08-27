package service

import (
	"strings"
	"testing"
)

/*
A subId becomes a path segment of the subscription URL, so some characters
break the very link they are meant to address.

`#` truncates the URL to a fragment, `?` starts a query, and `%` opens an escape
the router decodes before the lookup — so the stored id no longer matches what
arrives. The client gets a 404 for a subscription that exists.

Two boundaries are deliberate and both are pinned below.

FIRST: this is a subId-specific rule, not a widening of hasForbiddenClientChar.
That helper is shared with email validation, so tightening it would silently
start refusing email addresses.

SECOND: '&' and '+' are NOT banned. Both are legal literals in a path segment
and round-trip today, so refusing them would be a regression dressed as
hardening. F174.
*/
func TestASubIDThatBreaksItsOwnSubscriptionURLIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		subID   string
		refused bool
		why     string
	}{
		{"a fragment marker", "abc#def", true, "everything after # never reaches the server"},
		{"a query marker", "abc?def", true, "the router sees abc and a query string"},
		{"a percent escape", "abc%2Fdef", true, "the router decodes it before the lookup, so it cannot match the stored id"},
		{"an ampersand", "abc&def", false, "legal in a path segment and round-trips today; banning it is a regression"},
		{"a plus", "abc+def", false, "same"},
		{"an equals", "abc=def", false, "same"},
		{"a semicolon", "abc;def", false, "same"},
		{"an ordinary generated id", "754ytvldzbl1tmza", false, "the shape the panel mints"},
		{"empty", "", false, "means 'unset' and is filled in from the existing row"},
		{"a slash, which the shared rule already refused", "abc/def", true, "this one is not new"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateNewClientSubID(c.subID)
			if c.refused {
				if err == nil {
					t.Fatalf("accepted a subId that breaks its own subscription URL: %s", c.why)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a workable subId (%s): %v", c.why, err)
			}
		})
	}
}

/*
And a value merely being re-saved keeps the old, looser rule.

Update and import both re-validate a stored id. Applying the new characters
there would take a client that is currently un-fetchable and make it uneditable
and unimportable as well — strictly worse for the operator trying to fix it.
*/
func TestAStoredSubIDStaysEditable(t *testing.T) {
	legacy := "legacy#id"

	if err := validateClientSubID(legacy); err != nil {
		t.Fatalf("a stored subId became invalid to re-save, so the client holding it can no longer be edited at all: %v", err)
	}
	if err := validateNewClientSubID(legacy); err == nil {
		t.Fatal("the stricter rule accepted the same value, so setting one anew is still possible")
	} else if !strings.Contains(err.Error(), "subscription URL") {
		t.Fatalf("the refusal should say what it breaks, got: %v", err)
	}
}
