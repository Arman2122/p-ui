package service

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCurrentStatusHasAnInitialSnapshot(t *testing.T) {
	initTestDB(t)
	s := &ServerService{cachedIPv4: "198.51.100.1", cachedIPv6: "N/A", noIPv6: true, cachedCpuSpeedMhz: 1000}
	status := s.CurrentStatus()
	if status == nil || status.PanelGuid == "" || status.PanelVersion == "" || status.ObservedAt == 0 {
		t.Fatalf("cold status = %+v, want an identified initial snapshot", status)
	}
	if s.LastStatus() != status || s.CurrentStatus() != status {
		t.Fatal("cold sample was not cached")
	}
}

func TestStatusSamplingDoesNotWaitForPublicIP(t *testing.T) {
	initTestDB(t)
	started, release := make(chan struct{}), make(chan struct{})
	var startedOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		_, _ = w.Write([]byte("198.51.100.9"))
	}))
	defer srv.Close()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	previous := publicIPv4Services
	publicIPv4Services = []string{srv.URL}
	t.Cleanup(func() { publicIPv4Services = previous })
	s := &ServerService{noIPv6: true, cachedCpuSpeedMhz: 1000}
	done := make(chan *Status, 1)
	go func() { done <- s.RefreshStatus() }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("public IP discovery never started")
	}
	select {
	case status := <-done:
		if status == nil {
			t.Fatal("status sample is nil")
		}
	case <-time.After(2 * time.Second):
		unblock()
		<-done
		t.Fatal("status waited for a public-IP HTTP response")
	}
}

func TestConcurrentStatusRefresh(t *testing.T) {
	initTestDB(t)
	if _, err := (&SettingService{}).GetPanelGuid(); err != nil {
		t.Fatal(err)
	}
	s := &ServerService{cachedIPv4: "198.51.100.1", cachedIPv6: "N/A", noIPv6: true}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			if status := s.RefreshStatus(); status == nil || status.ObservedAt == 0 {
				t.Errorf("refresh = %+v", status)
			}
		})
	}
	workers.Wait()
}
