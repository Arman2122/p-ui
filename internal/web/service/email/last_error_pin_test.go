package email

import (
	"os"
	"strings"
	"testing"
)

// Send must record every outcome, the test button's included: the record is
// what the Email tab shows. Feature gap O13.
func TestSendRecordsItsOutcome(t *testing.T) {
	body, err := os.ReadFile("email.go")
	if err != nil {
		t.Fatalf("read email.go: %v", err)
	}
	src := string(body)
	send := strings.Index(src, "func (s *EmailService) Send(subject, body string) error {")
	if send < 0 {
		t.Fatal("Send not found")
	}
	fn := src[send:]
	if end := strings.Index(fn, "\nfunc "); end > 0 {
		fn = fn[:end]
	}
	if !strings.Contains(fn, "s.settingService.RecordSmtpDelivery(err)") {
		t.Fatal("Send does not record the delivery outcome")
	}
}
