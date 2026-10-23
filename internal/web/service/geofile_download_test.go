package service

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadGeofileVerifiesPublishedArtifact(t *testing.T) {
	valid := geofile([]byte("complete record"))
	for _, tc := range []struct {
		name       string
		body       []byte
		digest     string
		status     int
		wantError  string
		wantChange bool
	}{
		{"verified", valid, fmt.Sprintf("%x  release/geoip.dat\n", sha256.Sum256(valid)), 200, "", true},
		{"mismatch", valid, strings.Repeat("0", 64) + "  geoip.dat\n", 200, "SHA-256 mismatch", false},
		{"wrong asset", valid, fmt.Sprintf("%x  geosite.dat\n", sha256.Sum256(valid)), 200, "no SHA-256 checksum for geoip.dat", false},
		{"malformed digest", valid, "deadbeef  geoip.dat\n", 200, "invalid SHA-256 checksum", false},
		{"missing digest", valid, "", 404, "checksum: unexpected HTTP 404", false},
		{"oversized digest", valid, strings.Repeat("x", maxGeofileDigestBytes+1), 200, "checksum exceeds", false},
		{"truncated data with matching digest", valid[:len(valid)-1], fmt.Sprintf("%x  geoip.dat\n", sha256.Sum256(valid[:len(valid)-1])), 200, "middle of a record", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/repo/releases/latest/download/geoip.dat", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/repo/releases/download/v1/geoip.dat", http.StatusFound)
			})
			mux.HandleFunc("/repo/releases/download/v1/geoip.dat", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(tc.body)
			})
			mux.HandleFunc("/repo/releases/download/v1/geoip.dat.sha256sum", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.digest)
			})
			mux.HandleFunc("/repo/releases/latest/download/geoip.dat.sha256sum", func(w http.ResponseWriter, _ *http.Request) {
				t.Error("checksum was not pinned to the downloaded release")
				w.WriteHeader(http.StatusNotFound)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			dest := filepath.Join(t.TempDir(), "geoip_IR.dat")
			if err := os.WriteFile(dest, []byte("previous"), 0o600); err != nil {
				t.Fatal(err)
			}
			changed, err := downloadGeofile(srv.Client(), srv.URL+"/repo/releases/latest/download/geoip.dat", dest)
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
			if changed != tc.wantChange {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChange)
			}
			want := "previous"
			if changed {
				want = string(valid)
			}
			if body, err := os.ReadFile(dest); err != nil || string(body) != want {
				t.Fatalf("installed bytes = %q, err = %v, want %q", body, err, want)
			}
			entries, err := os.ReadDir(filepath.Dir(dest))
			if err != nil || len(entries) != 1 {
				t.Fatalf("staging debris remains: %v (%v)", entries, err)
			}
		})
	}
}

func TestDownloadGeofileNotModifiedAndSizeLimit(t *testing.T) {
	for _, status := range []int{http.StatusNotModified, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/latest/") {
					http.Redirect(w, r, "/repo/releases/download/v1/geoip.dat", http.StatusFound)
					return
				}
				if strings.HasSuffix(r.URL.Path, ".sha256sum") {
					t.Error("unchanged or oversized asset fetched a checksum")
				}
				if r.Header.Get("If-Modified-Since") == "" {
					t.Error("missing conditional GET")
				}
				if status == http.StatusOK {
					w.Header().Set("Content-Length", fmt.Sprint(maxGeofileBytes+1))
				}
				w.WriteHeader(status)
			}))
			defer srv.Close()
			dest := filepath.Join(t.TempDir(), "geoip.dat")
			if err := os.WriteFile(dest, []byte("previous"), 0o600); err != nil {
				t.Fatal(err)
			}
			changed, err := downloadGeofile(srv.Client(), srv.URL+"/repo/releases/latest/download/geoip.dat", dest)
			if status == http.StatusNotModified && err != nil || status == http.StatusOK && (err == nil || err.Error() != fmt.Sprintf("geofile exceeds %d bytes", maxGeofileBytes)) {
				t.Fatalf("error = %v for status %d", err, status)
			}
			if changed {
				t.Fatal("unchanged or oversized asset reported an install")
			}
		})
	}
}

func TestGeofileReleasePinRefusesAnotherArtifact(t *testing.T) {
	for _, location := range []string{
		"https://other.invalid/repo/releases/download/v1/geoip.dat",
		"/other/releases/download/v1/geoip.dat",
		"/repo/releases/download/v1/geosite.dat",
	} {
		t.Run(location, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, location, http.StatusFound)
			}))
			defer srv.Close()
			_, err := pinGeofileAssetURL(srv.Client(), srv.URL+"/repo/releases/latest/download/geoip.dat")
			if err == nil || !strings.Contains(err.Error(), "unexpected geofile release redirect") {
				t.Fatalf("error = %v, want another-artifact refusal", err)
			}
		})
	}
}
