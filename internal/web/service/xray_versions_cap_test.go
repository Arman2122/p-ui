package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

/*
The GitHub release list is read under a cap, like every other body the panel fetches.

GetXrayVersions buffered the whole response before parsing it; the archive,
binary and digest reads all have a ceiling and this one, from the same host,
had none. A list larger than the cap is refused instead of buffered. F285.
*/
func TestXrayReleaseListLargerThanTheCapIsRefused(t *testing.T) {
	initTestDB(t)
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "[")
		for i := 0; i <= maxXrayReleasesBytes/24; i++ {
			_, _ = fmt.Fprintf(w, `{"tag_name":"v25.%d.0"},`, i)
		}
		_, _ = io.WriteString(w, `{"tag_name":"v25.9.9"}]`)
	}))
	t.Cleanup(huge.Close)
	small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"tag_name":"v25.1.2"},{"tag_name":"v25.1.1"}]`)
	}))
	t.Cleanup(small.Close)
	previous := xrayReleasesURL
	t.Cleanup(func() { xrayReleasesURL = previous })

	s := &ServerService{}
	xrayReleasesURL = huge.URL
	if _, err := s.GetXrayVersions(); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("a release list over the cap: err = %v, want a refusal naming the cap", err)
	}

	xrayReleasesURL = small.URL
	got, err := s.GetXrayVersions()
	if err != nil {
		t.Fatalf("a normal release list: %v", err)
	}
	if strings.Join(got, ",") != "25.1.2,25.1.1" {
		t.Fatalf("versions = %v, want [25.1.2 25.1.1]", got)
	}
}
