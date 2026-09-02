package email

import (
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/eventbus"
)

/*
An alert mail is HTML, and the values it reports are text.

kv interpolated an outbound tag, a node name and xray's own error text straight
into <p> markup, so one carrying `<` garbled the mail -- and a tag written as
`<img src=x onerror=...>` was delivered as exactly that to a mail client that
renders HTML. The status lines that deliberately carry a coloured <span> are
the one place markup is intended, and they keep it. F360.
*/
func TestAlertMailEscapesTheValuesItReports(t *testing.T) {
	_, body := (&Subscriber{}).formatMessage(eventbus.Event{
		Type:      eventbus.EventOutboundDown,
		Source:    `<img src=x onerror=alert(1)>`,
		Data:      &eventbus.OutboundHealthData{Error: `dial: <script>x</script>`},
		Timestamp: time.Now(),
	})

	for _, forbidden := range []string{"<img", "<script>x"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body still carries %q as markup:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(body, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Fatalf("body does not show the tag as text:\n%s", body)
	}
	// The status line is this file's own markup and must survive escaping.
	if !strings.Contains(body, `<span style="color:red">`) {
		t.Fatalf("the status span was escaped away:\n%s", body)
	}
}
