package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
)

const rotationPaidLicense = "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"

func setupWarpRotation(t *testing.T, rejectLicense bool) (*WarpService, *atomic.Int32) {
	t.Helper()
	initTestDB(t)
	licenseCalls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/reg":
			_, _ = w.Write([]byte(`{"id":"new-device","token":"new-token","account":{"license":""},"config":{"client_id":"YWJj"}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/reg/new-device/account":
			licenseCalls.Add(1)
			if rejectLicense {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"errors":[{"message":"license rate limited"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"new-device"}`))
		default:
			t.Errorf("unexpected WARP request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	previous := warpAPIBase
	warpAPIBase = server.URL
	t.Cleanup(func() { warpAPIBase = previous })
	svc := &WarpService{}
	if err := svc.SetWarp(`{"access_token":"old-token","device_id":"old-device","license_key":"` + rotationPaidLicense + `","private_key":"old-key"}`); err != nil {
		t.Fatal(err)
	}
	return svc, licenseCalls
}

func TestWarpRotationReturnsPreservedLicenseAndUpgradeWarning(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		name := "license accepted"
		if rejected {
			name = "license rejected"
		}
		t.Run(name, func(t *testing.T) {
			svc, calls := setupWarpRotation(t, rejected)
			raw, err := svc.ChangeWarpIP()
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Data    map[string]string `json:"data"`
				Warning string            `json:"warning"`
			}
			if err := json.Unmarshal([]byte(raw), &response); err != nil {
				t.Fatal(err)
			}
			if response.Data["license_key"] != rotationPaidLicense || response.Data["device_id"] != "new-device" {
				t.Errorf("rotation response = %v, want new device and preserved license", response.Data)
			}
			wantWarning := ""
			if rejected {
				wantWarning = "failed to re-apply WARP license: license rate limited"
			}
			if response.Warning != wantWarning {
				t.Errorf("rotation warning = %q, want %q", response.Warning, wantWarning)
			}
			stored, err := svc.loadWarpCreds()
			if err != nil || stored["license_key"] != rotationPaidLicense || calls.Load() != 1 {
				t.Fatalf("stored license=%q, calls=%d, err=%v", stored["license_key"], calls.Load(), err)
			}
		})
	}
}

func TestWarpRotationStopsWhenPreservedLicenseCannotBeSaved(t *testing.T) {
	svc, calls := setupWarpRotation(t, false)
	if err := database.GetDB().Exec(`CREATE FUNCTION reject_paid_warp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.key = 'warp' AND NEW.value::jsonb->>'device_id' = 'new-device' AND NEW.value::jsonb->>'license_key' <> '' THEN
    RAISE EXCEPTION 'preserved WARP license write rejected';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER reject_paid_warp BEFORE UPDATE ON settings FOR EACH ROW EXECUTE FUNCTION reject_paid_warp()`).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := svc.ChangeWarpIP()
	if err == nil || !strings.Contains(err.Error(), "preserved WARP license write rejected") || raw != "" {
		t.Fatalf("failed preservation returned %q, %v", raw, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("attempted %d license upgrades after failed credential persistence", calls.Load())
	}
}
