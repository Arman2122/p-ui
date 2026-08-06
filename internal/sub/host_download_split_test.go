package sub

import (
	"encoding/json"
	"testing"
)

// splitStream builds the per-host stream an XHTTP inbound with a split
// download endpoint renders into.
func splitStream(mode string) map[string]any {
	return map[string]any{
		"network": "xhttp",
		"xhttpSettings": map[string]any{
			"path": "/upload",
			"mode": mode,
			"downloadSettings": map[string]any{
				"address": "download.example.com",
				"port":    float64(443),
				"xhttpSettings": map[string]any{
					"path": "/upload",
				},
			},
		},
	}
}

func downloadOf(t *testing.T, stream map[string]any) map[string]any {
	t.Helper()
	xhttp, ok := stream["xhttpSettings"].(map[string]any)
	if !ok {
		t.Fatalf("stream lost its xhttpSettings: %#v", stream)
	}
	d, _ := xhttp["downloadSettings"].(map[string]any)
	return d
}

/*
A host is a second way in to one inbound, and only the upload half moves with
it. Without an override the alternate entry point uploads to the host and
downloads from the address the inbound was built with — a pairing that was
never deployed, failing in the direction carrying the bytes.
*/
func TestHostOverridesTheSplitDownloadEndpoint(t *testing.T) {
	override, err := json.Marshal(map[string]any{
		"address": "cdn-download.example.com",
		"port":    8443,
	})
	if err != nil {
		t.Fatalf("marshal override: %v", err)
	}

	stream := splitStream("stream-up")
	applyHostStreamOverrides(map[string]any{
		"isHost":           true,
		"downloadSettings": string(override),
	}, stream)

	got := downloadOf(t, stream)
	if got["address"] != "cdn-download.example.com" {
		t.Errorf("host download address = %v, want the host's own — otherwise this entry point downloads from a server it was never paired with", got["address"])
	}
	if got["port"] != float64(8443) {
		t.Errorf("host download port = %v, want 8443", got["port"])
	}
}

// A host that only moves the path must move it on BOTH halves, or the two
// directions disagree about where they are talking to.
func TestHostPathReachesBothHalvesOfASplit(t *testing.T) {
	stream := splitStream("stream-up")
	applyHostStreamOverrides(map[string]any{
		"isHost":     true,
		"path":       "/edge",
		"hostHeader": "edge.example.com",
	}, stream)

	xhttp := stream["xhttpSettings"].(map[string]any)
	if xhttp["path"] != "/edge" {
		t.Errorf("upload path = %v, want /edge", xhttp["path"])
	}
	inner, ok := downloadOf(t, stream)["xhttpSettings"].(map[string]any)
	if !ok {
		t.Fatal("download half lost its xhttpSettings")
	}
	if inner["path"] != "/edge" {
		t.Errorf("download path = %v, want /edge — the halves must agree on the path the host set", inner["path"])
	}
	if inner["host"] != "edge.example.com" {
		t.Errorf("download host header = %v, want edge.example.com", inner["host"])
	}
}

// stream-one plus a download endpoint is a config xray-core declines to start
// on, so a host must never assemble one.
func TestHostDropsTheSplitInStreamOne(t *testing.T) {
	stream := splitStream("stream-one")
	applyHostStreamOverrides(map[string]any{"isHost": true, "path": "/edge"}, stream)

	if d := downloadOf(t, stream); d != nil {
		t.Errorf("stream-one host kept a download endpoint xray-core will not start with: %#v", d)
	}
}
