package locale

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

/*
A request's language must be that request's, and nobody else's.

LocalizerMiddleware assigned a package-level LocalizerWeb on every request and
I18n read it back, so the language of a response was whichever request had run
the middleware most recently. The same middleware is mounted on BOTH HTTP
servers in this process, which makes the usual case a subscriber poll: every one
of them rewrote the global with that subscriber's cookie or Accept-Language, and
an admin's page rendered in it.

It was a data race as well — an unsynchronised write on every request against a
read on every localized string — which is what makes it a `go test -race`
failure waiting for enough traffic.

Two concurrent requests with different languages, and each must get its own.
F407.
*/
func TestEachRequestGetsItsOwnLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	i18nBundle = i18n.NewBundle(language.MustParse("en-US"))
	i18nBundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	mustAddMessages(t, "en-US", `{"greeting":"hello"}`)
	mustAddMessages(t, "fa-IR", `{"greeting":"salaam"}`)

	router := gin.New()
	router.Use(LocalizerMiddleware())
	router.GET("/greet", func(c *gin.Context) {
		fn, ok := c.Get("I18n")
		if !ok {
			t.Error("the middleware set no I18n function")
			return
		}
		localize := fn.(func(i18nType I18nType, key string, params ...string) string)
		c.String(http.StatusOK, localize(Web, "greeting"))
	})

	ask := func(lang string) string {
		req := httptest.NewRequest(http.MethodGet, "/greet", nil)
		req.Header.Set("Accept-Language", lang)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		return resp.Body.String()
	}

	// Serially first: the two languages really are distinguishable here.
	if got := ask("en-US"); got != "hello" {
		t.Fatalf("en-US greeting = %q, want %q", got, "hello")
	}
	if got := ask("fa-IR"); got != "salaam" {
		t.Fatalf("fa-IR greeting = %q, want %q", got, "salaam")
	}

	// And under concurrency, which is where the global lost.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wrong := 0
	for range 40 {
		for lang, want := range map[string]string{"en-US": "hello", "fa-IR": "salaam"} {
			wg.Add(1)
			go func(lang, want string) {
				defer wg.Done()
				if got := ask(lang); got != want {
					mu.Lock()
					wrong++
					mu.Unlock()
				}
			}(lang, want)
		}
	}
	wg.Wait()

	if wrong > 0 {
		t.Fatalf("%d of 80 concurrent requests were answered in another request's language", wrong)
	}
}

func mustAddMessages(t *testing.T, tag, body string) {
	t.Helper()
	if _, err := i18nBundle.ParseMessageFileBytes([]byte(body), tag+".json"); err != nil {
		t.Fatalf("load %s: %v", tag, err)
	}
}
