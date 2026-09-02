package service

import (
	"errors"
	"testing"
)

/*
A failed email notification was a log line and nothing else, so an operator
whose SMTP password had expired found out from the silence. The mailer now
records its last failure where the Email tab shows it, and clears it on the
next success. Feature gap O13.
*/
func TestSmtpDeliveryOutcomeIsKeptForTheEmailTab(t *testing.T) {
	initTestDB(t)
	svc := &SettingService{}
	if err := svc.RecordSmtpDelivery(errors.New("dial tcp 10.0.0.9:587: connection refused")); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	msg, at, err := svc.GetSmtpLastError()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg != "dial tcp 10.0.0.9:587: connection refused" || at <= 0 {
		t.Fatalf("last error = %q at %d, want the failure with a timestamp", msg, at)
	}
	if err := svc.RecordSmtpDelivery(nil); err != nil {
		t.Fatalf("record success: %v", err)
	}
	msg, at, err = svc.GetSmtpLastError()
	if err != nil {
		t.Fatalf("read after success: %v", err)
	}
	if msg != "" || at != 0 {
		t.Fatalf("after a success the record must be clear, got %q at %d", msg, at)
	}
}
