package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
The list said "disabled" and nothing else, so an operator could not tell a
client the quota job switched off from one a colleague turned off by hand.
The row now names the cause: the quota it used up wins over an expiry it
also passed, and a client off for neither reason is off by hand.
Feature gap O8.
*/
func TestDisabledReasonNamesWhatKeepsAClientOff(t *testing.T) {
	const now = int64(1_800_000_000_000)
	off := &model.ClientRecord{Enable: false}
	tests := []struct {
		name string
		rec  *model.ClientRecord
		t    *xray.ClientTraffic
		want string
	}{
		{name: "enabled", rec: &model.ClientRecord{Enable: true}, t: &xray.ClientTraffic{Total: 1, Up: 2}, want: ""},
		{name: "quota used up", rec: off, t: &xray.ClientTraffic{Total: 100, Up: 60, Down: 40}, want: "quota"},
		{name: "quota wins over a passed expiry", rec: off, t: &xray.ClientTraffic{Total: 100, Up: 100, ExpiryTime: now - 1}, want: "quota"},
		{name: "expired", rec: off, t: &xray.ClientTraffic{Total: 100, Up: 1, ExpiryTime: now - 1}, want: "expiry"},
		{name: "future expiry and quota left", rec: off, t: &xray.ClientTraffic{Total: 100, Up: 1, ExpiryTime: now + 1}, want: "manual"},
		{name: "unlimited and off", rec: off, t: &xray.ClientTraffic{}, want: "manual"},
		{name: "no traffic row", rec: off, t: nil, want: "manual"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := disabledReason(tc.rec, tc.t, now); got != tc.want {
				t.Fatalf("disabledReason = %q, want %q", got, tc.want)
			}
		})
	}
}
