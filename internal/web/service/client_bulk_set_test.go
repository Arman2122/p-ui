package service

import (
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
Bulk adjust can SET a value, not only add to one.

ADD answers "give everyone another 30 days"; SET answers "make it exactly 30
days from now", and reaching that through ADD needs the operator to know each
client's current value. The two are different questions and the dialog now asks
which one is being asked.

Most of ADD's skips do not apply to SET, deliberately: adding to an unlimited
client means nothing, but SETTING a quota on one is exactly the request, and
nothing underflows because nothing is subtracted. What SET keeps is the zero
convention -- 0 leaves the field alone -- because the form's fields start at
zero, so filling in only a quota must not strip the expiry off the selection.
*/
const gib = int64(1024 * 1024 * 1024)

/*
The record AND the counter row, because BulkAdjust reads the plan off clients
and writes the values onto client_traffics -- the quota and expiry a client is
actually judged against live there.
*/
func seedBulkClient(t *testing.T, email string, totalGB, expiry int64) {
	t.Helper()
	db := database.GetDB()
	rec := &model.ClientRecord{Email: email, Enable: true, TotalGB: totalGB, ExpiryTime: expiry}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("seed %s: %v", email, err)
	}
	traffic := &xray.ClientTraffic{Email: email, Enable: true, Total: totalGB, ExpiryTime: expiry}
	if err := db.Create(traffic).Error; err != nil {
		t.Fatalf("seed traffic for %s: %v", email, err)
	}
}

func reloadBulk(t *testing.T, email string) *xray.ClientTraffic {
	t.Helper()
	row := &xray.ClientTraffic{}
	if err := database.GetDB().Where("email = ?", email).First(row).Error; err != nil {
		t.Fatalf("reload %s: %v", email, err)
	}
	return row
}

func skipReason(res BulkAdjustResult, email string) string {
	for _, s := range res.Skipped {
		if s.Email == email {
			return s.Reason
		}
	}
	return ""
}

// SET writes the value; ADD would have added to whatever was there.
func TestBulkSetWritesTheQuotaRatherThanAddingToIt(t *testing.T) {
	setupBulkDB(t)
	seedBulkClient(t, "has@x", 100*gib, 0)

	svc := &ClientService{}
	res, _, err := svc.BulkAdjust(&InboundService{}, []string{"has@x"}, 0, 50*gib, "", BulkModeSet)
	if err != nil {
		t.Fatalf("BulkAdjust: %v", err)
	}
	if res.Adjusted != 1 {
		t.Fatalf("adjusted %d, want 1 (skipped: %v)", res.Adjusted, res.Skipped)
	}
	if got := reloadBulk(t, "has@x").Total; got != 50*gib {
		t.Fatalf("Total = %d, want exactly %d; SET added instead of writing", got, 50*gib)
	}
}

/*
And an unlimited client is SET, not skipped.

ADD skips it because adding to unlimited is meaningless. That reasoning does not
carry: "make everyone 50 GB" is precisely the operator asking to limit the
clients who currently have none.
*/
func TestBulkSetLimitsAnUnlimitedClient(t *testing.T) {
	setupBulkDB(t)
	seedBulkClient(t, "unlimited@x", 0, 0)

	svc := &ClientService{}
	res, _, err := svc.BulkAdjust(&InboundService{}, []string{"unlimited@x"}, 0, 50*gib, "", BulkModeSet)
	if err != nil {
		t.Fatalf("BulkAdjust: %v", err)
	}
	if res.Adjusted != 1 {
		t.Fatalf("an unlimited client was skipped by SET: %v", res.Skipped)
	}
	if got := reloadBulk(t, "unlimited@x").Total; got != 50*gib {
		t.Fatalf("Total = %d, want %d", got, 50*gib)
	}
}

// ADD still skips it, because that half must not change.
func TestBulkAddStillSkipsAnUnlimitedClient(t *testing.T) {
	setupBulkDB(t)
	seedBulkClient(t, "unlimited2@x", 0, 0)

	svc := &ClientService{}
	res, _, err := svc.BulkAdjust(&InboundService{}, []string{"unlimited2@x"}, 0, 50*gib, "", BulkModeAdd)
	if err != nil {
		t.Fatalf("BulkAdjust: %v", err)
	}
	if res.Adjusted != 0 || skipReason(res, "unlimited2@x") != "unlimited traffic" {
		t.Fatalf("ADD no longer skips an unlimited client: adjusted=%d skipped=%v", res.Adjusted, res.Skipped)
	}
}

/*
Zero leaves the field alone, which is the whole safety of the mode.

The dialog's fields start at zero, so an operator setting only a quota sends
days=0. Reading that as "make the expiry unlimited" would strip the expiry from
every client in the selection, silently, on the most ordinary use of the form.
*/
func TestBulkSetLeavesAZeroFieldUntouched(t *testing.T) {
	setupBulkDB(t)
	expiry := time.Now().Add(72 * time.Hour).UnixMilli()
	seedBulkClient(t, "keep@x", 100*gib, expiry)

	svc := &ClientService{}
	if _, _, err := svc.BulkAdjust(&InboundService{}, []string{"keep@x"}, 0, 50*gib, "", BulkModeSet); err != nil {
		t.Fatalf("BulkAdjust: %v", err)
	}
	if got := reloadBulk(t, "keep@x").ExpiryTime; got != expiry {
		t.Fatalf("ExpiryTime = %d, want the untouched %d; a quota-only SET moved the expiry", got, expiry)
	}
}

// SET days is a date counted from now, the way the create form computes one.
func TestBulkSetExpiryIsCountedFromNow(t *testing.T) {
	setupBulkDB(t)
	seedBulkClient(t, "expiry@x", 0, time.Now().Add(1000*time.Hour).UnixMilli())

	before := time.Now().UnixMilli()
	svc := &ClientService{}
	if _, _, err := svc.BulkAdjust(&InboundService{}, []string{"expiry@x"}, 30, 0, "", BulkModeSet); err != nil {
		t.Fatalf("BulkAdjust: %v", err)
	}
	got := reloadBulk(t, "expiry@x").ExpiryTime
	thirtyDays := int64(30) * 24 * 60 * 60 * 1000
	if got < before+thirtyDays || got > time.Now().UnixMilli()+thirtyDays {
		t.Fatalf("ExpiryTime = %d, want roughly now+30d; SET treated the days as an offset from the stored value", got)
	}
}

/*
A delayed-start client is skipped and told why.

Its expiry is stored negative: a countdown that begins on first connection, not
a date. Writing an absolute date over it would silently convert one product into
another for everyone in the selection.
*/
func TestBulkSetSkipsADelayedStartClient(t *testing.T) {
	setupBulkDB(t)
	const delayed = int64(-86400000 * 7)
	seedBulkClient(t, "delayed@x", 0, delayed)

	svc := &ClientService{}
	res, _, err := svc.BulkAdjust(&InboundService{}, []string{"delayed@x"}, 30, 0, "", BulkModeSet)
	if err != nil {
		t.Fatalf("BulkAdjust: %v", err)
	}
	if res.Adjusted != 0 {
		t.Fatal("SET converted a delayed-start client to an absolute date")
	}
	if got := skipReason(res, "delayed@x"); got != "delayed start" {
		t.Fatalf("skip reason = %q, want %q", got, "delayed start")
	}
	if got := reloadBulk(t, "delayed@x").ExpiryTime; got != delayed {
		t.Fatalf("the delayed-start window was rewritten: %d, want %d", got, delayed)
	}
}

// An absent mode is ADD, so every caller that predates this keeps its meaning.
func TestAnAbsentModeIsAdd(t *testing.T) {
	setupBulkDB(t)
	seedBulkClient(t, "legacy@x", 100*gib, 0)

	svc := &ClientService{}
	if _, _, err := svc.BulkAdjust(&InboundService{}, []string{"legacy@x"}, 0, 50*gib, "", ""); err != nil {
		t.Fatalf("BulkAdjust: %v", err)
	}
	if got := reloadBulk(t, "legacy@x").Total; got != 150*gib {
		t.Fatalf("Total = %d, want the ADD result %d; an omitted mode changed meaning", got, 150*gib)
	}
}

// An unknown mode is refused rather than coerced into one of them.
func TestAnUnknownModeIsRefused(t *testing.T) {
	setupBulkDB(t)
	seedBulkClient(t, "who@x", 100*gib, 0)

	svc := &ClientService{}
	_, _, err := svc.BulkAdjust(&InboundService{}, []string{"who@x"}, 0, 50*gib, "", "multiply")
	if err == nil {
		t.Fatal("an unknown mode was accepted; a typo in an API call would silently add or set")
	}
	if got := reloadBulk(t, "who@x").Total; got != 100*gib {
		t.Fatalf("the refused call still wrote: Total = %d", got)
	}
}
