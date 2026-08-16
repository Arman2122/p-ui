package sub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

const fileShareServerKey = "aP2niiHV0Ao0ZBRDvBrEG4XeAAJmyzWonh9eNe4ZaVw="

// seedFileShareInbound writes a wgkernel inbound whose one client carries a key
// and a subscription id, through the same rows production writes.
func seedFileShareInbound(t *testing.T, subId string) *model.Inbound {
	t.Helper()
	db := database.GetDB()

	clients := []map[string]any{{
		"email": "u@wg", "enable": true, "subId": subId,
		"privateKey": "clientPrivateKey=", "allowedIPs": []string{"10.8.0.4/32"},
	}}
	settings, err := json.Marshal(map[string]any{"secretKey": fileShareServerKey, "dns": "9.9.9.9", "clients": clients})
	if err != nil {
		t.Fatal(err)
	}
	in := &model.Inbound{Port: 51820, Protocol: model.WGKernel, Enable: true, Tag: "wg-file", Settings: string(settings)}
	if err := db.Create(in).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	rec := &model.ClientRecord{Email: "u@wg", SubID: subId, Enable: true, PrivateKey: "clientPrivateKey=", AllowedIPs: "10.8.0.4/32"}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: in.Id}).Error; err != nil {
		t.Fatalf("create link: %v", err)
	}
	return in
}

func fileShareRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewSUBController(router.Group("/"))
	return router
}

/*
The subscription's file route is the first file-shaped delivery surface: a
WireGuard client is configured by a .conf no URI carries, and until this route
the panel had no way to hand one to the client it belongs to.
*/
func TestSubFileServesTheConf(t *testing.T) {
	initSubDB(t)
	in := seedFileShareInbound(t, "subfile")

	w := httptest.NewRecorder()
	fileShareRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/subfile/file/"+strconv.Itoa(in.Id), nil))

	if w.Code != 200 {
		t.Fatalf("GET file = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="u-wg.conf"`) {
		t.Fatalf("Content-Disposition = %q; the download must be named after the client", got)
	}
	body := w.Body.String()
	serverPub, err := wgutil.PublicKeyFromPrivate(fileShareServerKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[Interface]", "PrivateKey = clientPrivateKey=", "Address = 10.8.0.4/32",
		"DNS = 9.9.9.9", "[Peer]", "PublicKey = " + serverPub, "Endpoint = ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the served .conf is missing %q:\n%s", want, body)
		}
	}
}

// Every failure is the same 404: a guessable inbound id must not confirm that a
// subscription exists, and a wrong subscription must not learn an inbound does.
func TestSubFileRefusesWhatItCannotServe(t *testing.T) {
	initSubDB(t)
	in := seedFileShareInbound(t, "subfile")

	vless := &model.Inbound{Port: 443, Protocol: model.VLESS, Enable: true, Tag: "v", Settings: `{"clients":[{"email":"u@wg","enable":true,"subId":"subfile","id":"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}]}`}
	if err := database.GetDB().Create(vless).Error; err != nil {
		t.Fatal(err)
	}

	router := fileShareRouter()
	for name, path := range map[string]string{
		"wrong subscription":     "/sub/other/file/" + strconv.Itoa(in.Id),
		"unknown inbound":        "/sub/subfile/file/99999",
		"unparsable inbound id":  "/sub/subfile/file/nope",
		"a kind with no file":    "/sub/subfile/file/" + strconv.Itoa(vless.Id),
		"zero is never a row id": "/sub/subfile/file/0",
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 404 {
			t.Errorf("%s: GET %s = %d, want 404", name, path, w.Code)
		}
	}
}
