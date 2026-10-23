package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func TestRuntimeObservationsPreserveFailuresAndStripPrivateDetails(t *testing.T) {
	tests := []struct {
		id    string
		value *nodev1.RuntimeCondition
		want  string
	}{
		{"xray", &nodev1.RuntimeCondition{ResourceId: "xray", Condition: "CONFIGURATION_APPLIED", Ready: true, Detail: "private-runtime-credential"}, "CONFIGURATION_APPLIED"},
		{"ssh", &nodev1.RuntimeCondition{ResourceId: "ssh", Condition: "RESTORE_FAILED"}, "RESTORE_FAILED"},
		{"native", &nodev1.RuntimeCondition{ResourceId: "wrong-runtime", Condition: "UNCONFIGURED", Ready: true}, "STATUS_INVALID"},
		{"singbox", &nodev1.RuntimeCondition{ResourceId: "singbox", Condition: "RESTORE_FAILED", Ready: true}, "STATUS_INVALID"},
		{"mtproto", nil, "UNREACHABLE"},
	}
	local := new(LocalRuntimes)
	for _, test := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if test.value == nil {
				http.Error(w, "private-runtime-credential", http.StatusServiceUnavailable)
				return
			}
			encoded, _ := proto.Marshal(test.value)
			_, _ = w.Write(encoded)
		}))
		t.Cleanup(server.Close)
		client := server.Client()
		client.Transport = runtimeTestTransport{target: server.URL, base: client.Transport}
		local.workers = append(local.workers, localRuntime{endpoint: RuntimeEndpoint{ID: test.id}, client: client})
	}
	conditions := local.Conditions(context.Background())
	for i, condition := range conditions {
		if condition.Condition != tests[i].want || condition.ResourceId != tests[i].id || condition.Detail != "" || condition.Ready != (i == 0) {
			t.Fatalf("runtime %s observation = %+v", tests[i].id, condition)
		}
	}
}

type runtimeTestTransport struct {
	target string
	base   http.RoundTripper
}

func (r runtimeTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.URL.Host = strings.TrimPrefix(r.target, "http://")
	return r.base.RoundTrip(copy)
}
