package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// The wire the node reads: the door's path, the master's guid and the rows as
// the node's own model decodes them.
func TestPushBansHandsTheNodeTheWholeSetOnItsDoor(t *testing.T) {
	var gotPath string
	var got struct {
		MasterGuid string            `json:"masterGuid"`
		Bans       []model.ClientBan `json:"bans"`
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	r := NewRemote(nodeForServer(t, srv, "skip", ""), nil)
	rows := []model.ClientBan{{ClientEmail: "alice", Source: "203.0.113.7", Ports: "443,8443", Reason: "over-cap", Until: 1788700000}}
	if err := r.PushBans(context.Background(), "master-1", rows); err != nil {
		t.Fatalf("PushBans: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/panel/api/policies/pushBans") {
		t.Fatalf("pushed to %q, want the node's pushBans door", gotPath)
	}
	if got.MasterGuid != "master-1" || len(got.Bans) != 1 || got.Bans[0].Source != "203.0.113.7" || got.Bans[0].Ports != "443,8443" || got.Bans[0].Until != 1788700000 {
		t.Fatalf("the node decoded %+v, want the master's guid and the one row as sent", got)
	}
}
