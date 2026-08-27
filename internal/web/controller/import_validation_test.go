package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/middleware"
)

/*
/inbounds/import honoured no validate: tag at all.

addInbound and updateInbound both go through BindAndValidate, so every
`validate:` tag on model.Inbound — the port range, the protocol enum — is
enforced there. importInbound decoded a JSON string out of a form field with a
bare json.Unmarshal and never validated it, so the one endpoint that exists to
ingest a config from elsewhere was the one that checked nothing.

That is the gap CAMPAIGN-CLOSE §6 item 4 recorded as closed. An independent
audit of this campaign found the tree disagreed with the record; this closes it
for real. F031 / F312.

Driven through middleware.ValidateStruct rather than the HTTP handler, because
the handler needs a session, a database and a live core — none of which decides
whether a port of 70000 is refused.
*/
func TestAnImportedInboundIsValidatedLikeOneThatWasAdded(t *testing.T) {
	cases := []struct {
		name    string
		inbound model.Inbound
		refused bool
		why     string
	}{
		{
			name:    "a port above the range",
			inbound: model.Inbound{UserId: 1, Port: 70000, Protocol: model.VLESS, Tag: "t", Listen: "0.0.0.0"},
			refused: true,
			why:     "model.Inbound tags port lte=65535, and a port that cannot bind is a config xray refuses",
		},
		{
			name:    "a protocol that is not in the enum",
			inbound: model.Inbound{UserId: 1, Port: 443, Protocol: model.Protocol("not-a-protocol"), Tag: "t", Listen: "0.0.0.0"},
			refused: true,
			why:     "the protocol enum is what keeps an unknown kind out of the config builder",
		},
		{
			name:    "an ordinary inbound",
			inbound: model.Inbound{UserId: 1, Port: 443, Protocol: model.VLESS, Tag: "t", Listen: "0.0.0.0"},
			refused: false,
			why:     "the shape /inbounds/add would accept",
		},
	}

	gin.SetMode(gin.TestMode)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(url.Values{}.Encode()))

			inbound := c.inbound
			ok := middleware.ValidateStruct(ctx, &inbound)

			if c.refused && ok {
				t.Fatalf("the import endpoint accepted a config /inbounds/add would refuse: %s", c.why)
			}
			if !c.refused && !ok {
				t.Fatalf("refused a valid import (%s); body: %s", c.why, recorder.Body.String())
			}
		})
	}
}

// And the handler really calls it — the property a unit test on the seam alone
// cannot see, and the one that was missing for the whole of this endpoint's life.
func TestTheImportHandlerCallsTheValidator(t *testing.T) {
	body := readControllerSource(t, "inbound.go")

	start := strings.Index(body, "func (a *InboundController) importInbound(")
	if start < 0 {
		t.Fatal("importInbound was renamed; this guard is now vacuous")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of importInbound; this guard is now vacuous")
	}
	handler := body[start : start+end]

	if !strings.Contains(handler, "middleware.ValidateStruct(c, inbound)") {
		t.Fatal("importInbound decodes an inbound and never validates it, so every validate: tag on model.Inbound is unenforced on the one endpoint that ingests a config from elsewhere")
	}
	unmarshal := strings.Index(handler, "json.Unmarshal")
	guard := strings.Index(handler, "middleware.ValidateStruct")
	if unmarshal < 0 || guard < unmarshal {
		t.Fatal("the validation must follow the decode; validating an empty struct passes and proves nothing")
	}
}

// readControllerSource reads a file from this package for a source-level guard.
func readControllerSource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}
