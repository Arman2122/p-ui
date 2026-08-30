package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

/*
The server list kept only the servers that were BUSY.

GetServers filtered on `load > 7` — the condition upside down. The least loaded
servers, which are the ones worth picking, were the ones dropped; a country
quiet enough that every server sat at or below 7 came back empty, and a server
whose load field was missing or not a number was discarded as well. The operator
saw a short list, or none, and nothing said it had been filtered.

Not re-inverted with a threshold of its own: which load is too high is a policy
nobody chose, and the load is on every row for the operator to read. F356.
*/
func TestNordServersAreNotFilteredByLoad(t *testing.T) {
	initTestDB(t)

	const body = `{"servers":[` +
		`{"id":1,"name":"quiet","load":0},` +
		`{"id":2,"name":"light","load":7},` +
		`{"id":3,"name":"busy","load":83},` +
		`{"id":4,"name":"unknown-load"}` +
		`]}`

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v2/servers") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer stub.Close()

	previous := nordAPIBase
	nordAPIBase = stub.URL
	t.Cleanup(func() { nordAPIBase = previous })

	got, err := (&NordService{}).GetServers("202")
	if err != nil {
		t.Fatalf("GetServers: %v", err)
	}

	var parsed struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("the response is not the JSON Nord sent: %v\n%s", err, got)
	}
	names := make([]string, 0, len(parsed.Servers))
	for _, s := range parsed.Servers {
		names = append(names, s.Name)
	}
	if len(names) != 4 {
		t.Fatalf("servers = %v, want all four: the quiet ones are the ones worth picking, and they were the ones dropped", names)
	}
}

// And the calls go through the operator's egress proxy, like every other
// outbound request the panel makes — its sibling WARP integration in this same
// package always has. A behavioural test needs a running core to resolve the
// proxy URL, so this pins the call site.
func TestNordCallsUseThePanelsProxiedClient(t *testing.T) {
	source, err := os.ReadFile("nord.go")
	if err != nil {
		t.Fatalf("read nord.go: %v", err)
	}
	body := string(source)

	if strings.Contains(body, "nordHTTPClient") {
		t.Error("Nord still has its own package-level client on the default transport, so on a panel that reaches the internet only through its configured proxy the whole integration fails")
	}
	if n := strings.Count(body, "s.NewProxiedHTTPClient("); n != 3 {
		t.Errorf("%d of the 3 Nord calls use the panel's proxied client", n)
	}
}
