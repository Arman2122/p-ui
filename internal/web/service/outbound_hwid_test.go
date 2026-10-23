package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestOutboundFetchSendsPanelGuid(t *testing.T) {
	setupSettingTestDB(t)
	const want = "stable-panel-guid"
	if err := (&SettingService{}).setString("panelGuid", want); err != nil {
		t.Fatal(err)
	}
	headers := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("X-HWID")
		_, _ = w.Write([]byte("outbounds:\n"))
	}))
	defer srv.Close()
	sub := &model.OutboundSubscription{Url: srv.URL, AllowPrivate: true}
	if err := database.GetDB().Create(sub).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := NewOutboundSubscriptionService().Refresh(sub.Id); err != nil {
		t.Fatal(err)
	}
	if got := <-headers; got != want {
		t.Fatalf("X-HWID = %q, want %q", got, want)
	}
}
