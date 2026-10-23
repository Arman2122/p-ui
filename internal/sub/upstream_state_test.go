package sub

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

func seedInactiveExternalOnlySub(t *testing.T, subID, email string, enabled bool, expiry int64) {
	t.Helper()
	db := database.GetDB()
	rec := &model.ClientRecord{Email: email, SubID: subID, UUID: subID + "-uuid", Enable: true, ExpiryTime: expiry}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if !enabled {
		if err := db.Model(rec).Update("enable", false).Error; err != nil {
			t.Fatalf("disable client: %v", err)
		}
	}
	if err := db.Create(&xray.ClientTraffic{Email: email, Up: 11, Down: 22, Total: 1024, ExpiryTime: expiry}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
	link := "vless://11111111-1111-1111-1111-111111111111@example.com:443?type=tcp&security=reality&pbk=abc&sid=12&fp=chrome#external"
	if err := db.Create(&model.ClientExternalLink{ClientId: rec.Id, Kind: model.ExternalLinkKindLink, Value: link, SortIndex: 1}).Error; err != nil {
		t.Fatalf("seed external link: %v", err)
	}
}

func TestSub_HostTlsOverRealityDropsRealityParams(t *testing.T) {
	seedSubDB(t)
	reality := `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["master-dest.example.com"],"publicKey":"MASTERPBK","shortIds":["ab12"],"fingerprint":"chrome"}}`
	ib := seedSubInbound(t, "s1", "reality-in", 4461, 1, reality)
	seedHost(t, &model.Host{InboundId: ib.Id, SortOrder: 1, Remark: "H", Address: "edge.example.com", Port: 443, Security: "tls"})

	links, _, _, _, err := NewSubService("").GetSubs("s1", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	joined := strings.Join(links, "\n")
	if !strings.Contains(joined, "security=tls") {
		t.Fatalf("host forces tls, link must say so: %s", joined)
	}
	for _, leaked := range []string{"pbk=", "sid=", "spx=", "sni=master-dest.example.com"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("reality parameter %q survived a tls host override: %s", leaked, joined)
		}
	}
}

func TestResolveRequestKeepsHostBehindTrustedProxy(t *testing.T) {
	initSubDB(t)
	setTrustedProxyCIDRs(t, "10.0.0.0/8")
	c := requestFrom(t, "10.1.2.3:44000", map[string]string{"X-Real-IP": "198.51.100.7"})
	_, host, authority, display := (&SubService{}).ResolveRequest(c)
	if host != "panel.example.com" || authority != "panel.example.com:2096" || display != "panel.example.com" {
		t.Fatalf("host=%q authority=%q display=%q; want requested panel host", host, authority, display)
	}
}

func TestInactiveExternalSubscriptionHasNoCredentials(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		expiry  int64
	}{
		{"disabled", false, time.Now().Add(time.Hour).UnixMilli()},
		{"expired", true, time.Now().Add(-time.Hour).UnixMilli()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initSubDB(t)
			seedInactiveExternalOnlySub(t, "external-"+tc.name, tc.name+"@example.com", tc.enabled, tc.expiry)
			base := NewSubService("")
			links, _, _, traffic, err := base.GetSubs("external-"+tc.name, "sub.example.com")
			if err != nil {
				t.Fatal(err)
			}
			if len(links) != 0 || traffic.Total != 1024 {
				t.Errorf("raw links=%v traffic=%+v; want no links and known quota", links, traffic)
			}
			want := fmt.Sprintf("upload=11; download=22; total=1024; expire=%d", tc.expiry/1000)
			jsonBody, jsonHeader, err := NewSubJsonService("", "", "", base).GetJson("external-"+tc.name, "sub.example.com", false)
			if err != nil {
				t.Fatal(err)
			}
			if jsonBody != "" || jsonHeader != want {
				t.Errorf("JSON body=%q header=%q; want empty with %q", jsonBody, jsonHeader, want)
			}
			clashBody, clashHeader, err := NewSubClashService(false, "", false, base).GetClash("external-"+tc.name, "sub.example.com")
			if err != nil {
				t.Fatal(err)
			}
			if clashBody != "" || clashHeader != want {
				t.Errorf("Clash body=%q header=%q; want empty with %q", clashBody, clashHeader, want)
			}
		})
	}
}

func TestInactiveExternalSubscriptionHTTP(t *testing.T) {
	initSubDB(t)
	seedInactiveExternalOnlySub(t, "disabled-http", "disabled@example.com", false, 0)
	router := gin.New()
	NewSUBController(router.Group("/"), WithSUBJsonEnabled(true), WithSUBClashEnabled(true), WithSUBEncryption(false))
	for _, path := range []string{"/sub/disabled-http", "/json/disabled-http?view=raw", "/clash/disabled-http?view=raw"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Subscription-Userinfo") != "upload=11; download=22; total=1024; expire=0" {
				t.Fatalf("status=%d body=%q usage=%q", w.Code, w.Body.String(), w.Header().Get("Subscription-Userinfo"))
			}
		})
	}
}

func TestExternalFetchUsesStablePanelIdentity(t *testing.T) {
	initSubDB(t)
	if err := database.GetDB().Create(&model.Setting{Key: "panelGuid", Value: "panel-identity"}).Error; err != nil {
		t.Fatal(err)
	}
	received := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get("X-HWID")
		_, _ = w.Write([]byte("vless://id@host:443?security=none#node"))
	}))
	defer server.Close()
	for range 2 {
		links, err := doFetchSubscriptionLinks(server.URL)
		if err != nil || len(links) != 1 {
			t.Fatalf("links=%v error=%v", links, err)
		}
		if got := <-received; got != "panel-identity" {
			t.Fatalf("X-HWID=%q, want stable panel-identity", got)
		}
	}
}
