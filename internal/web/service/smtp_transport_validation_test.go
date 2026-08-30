package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/web/entity"
	"github.com/Arman2122/p-ui/internal/web/middleware"
)

/*
The mail transport is one of three words, and nothing said so.

smtpEncryptionType was stored with a bare setString and carried no validate tag,
so only the UI's Select constrained it. Anything else -- a typo over the API, a
value from an older build, an empty string -- is handled by neither switch that
reads it: SendEmail's default arm returns "unknown SMTP encryption type" for
every message the panel ever tries to send, while TestConnection's default arm
falls through to a PLAIN dial.

So the Test button reports a healthy connection and every expiry notice and
login alert fails, silently, for as long as the value stays wrong.

No omitempty, matching SmtpPort two lines above it: this struct is submitted
whole, and an empty transport is refused by both switches too. F353.
*/
func TestTheSmtpTransportMustBeOneTheCodeHandles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		value   string
		refused bool
	}{
		{value: "none"},
		{value: "starttls"},
		{value: "tls"},
		{value: "ssl", refused: true},
		{value: "STARTTLS", refused: true},
		{value: "", refused: true},
	}

	for _, tc := range cases {
		name := tc.value
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/setting/update", strings.NewReader(""))

			setting := &entity.AllSetting{
				WebPort: 2053, SubPort: 2096, SmtpPort: 587,
				SessionMaxAge: 60, OutboundDownThreshold: 5,
				SmtpEncryptionType: tc.value,
			}
			ok := middleware.ValidateStruct(ctx, setting)
			// Named, not merely refused: another field failing would otherwise
			// make this pass while the transport went unchecked.
			named := strings.Contains(recorder.Body.String(), "smtpEncryptionType")

			if tc.refused && (ok || !named) {
				t.Fatalf("smtpEncryptionType=%q was not refused for its own sake; neither switch that reads it handles that value, so every send fails while the Test button dials plain and reports success. body: %s", tc.value, recorder.Body.String())
			}
			if !tc.refused && !ok {
				t.Fatalf("smtpEncryptionType=%q was refused; it is one of the three the code handles. body: %s", tc.value, recorder.Body.String())
			}
		})
	}
}
