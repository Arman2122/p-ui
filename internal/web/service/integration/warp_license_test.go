package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

/*
Rotating the WARP IP must not throw away the operator's WARP+ licence.

ChangeWarpIP reuses RegWarp to get a fresh device, and RegWarp does double duty:
it registers AND commits that registration to the single stored `warp` blob,
including the brand-new device's own FREE licence key. From that moment the
panel holds no copy of the WARP+ key the operator paid for.

Only the very last statement puts it back, by re-applying it to Cloudflare -- and
that call is allowed to fail with nothing but a warning. Cloudflare rate-limits
registrations, so a rotation retried a few times is exactly when it fails. The
key is then gone from the panel: not degraded, not reported, gone, and the
operator's next rotation has nothing to re-apply.

The licence is restored into the stored blob BEFORE either exit, so a failed
re-apply leaves the panel still holding the key and the operator can retry. F362.
*/
func TestRotatingTheWarpIpKeepsTheOperatorsLicense(t *testing.T) {
	initTestDB(t)

	const paid = "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"
	const free = "11111111-2222-3333-4444-555555555555"

	// A stand-in Cloudflare: registration succeeds with a FREE licence, and the
	// licence re-apply fails the way a rate-limited one does.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/account") {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "new-device",
			"token": "new-token",
			"account": {"license": "` + free + `"},
			"config": {"client_id": "cid"}
		}`))
	}))
	t.Cleanup(srv.Close)

	orig := warpAPIBase
	warpAPIBase = srv.URL
	t.Cleanup(func() { warpAPIBase = orig })

	svc := &WarpService{}
	stored, err := json.Marshal(map[string]string{
		"access_token": "old-token",
		"device_id":    "old-device",
		"license_key":  paid,
		"private_key":  "cGxhY2Vob2xkZXJwbGFjZWhvbGRlcnBsYWNlaG9sZA==",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWarp(string(stored)); err != nil {
		t.Fatalf("seed the warp blob: %v", err)
	}

	// The re-apply fails; the rotation itself is allowed to report whatever it
	// likes. What must not happen is losing the key.
	_, _ = svc.ChangeWarpIP()

	after, err := svc.GetWarpData()
	if err != nil {
		t.Fatalf("read the warp blob back: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(after), &got); err != nil {
		t.Fatalf("stored warp blob is not readable: %v (%s)", err, after)
	}

	if got["license_key"] == free {
		t.Fatal("rotating the WARP IP replaced the operator's WARP+ licence with the new device's free one, and the re-apply that was meant to restore it failed with only a warning; the paid key is now gone from the panel entirely")
	}
	if got["license_key"] != paid {
		t.Fatalf("the stored licence is neither the operator's nor the free one: %q", got["license_key"])
	}
}
