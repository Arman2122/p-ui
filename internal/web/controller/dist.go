package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	htmlpkg "html"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

var distFS fs.FS

func SetDistFS(fsys fs.FS) {
	distFS = fsys
}

var distPageBuildTime = time.Now()

// ServeOpenAPISpec serves the embedded schema with the authenticated role's API surface.
// Shared components remain available so retained operations keep their schema references.
func ServeOpenAPISpec(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Writer.Header().Add("Vary", "Cookie, Authorization")
	user := session.GetLoginUser(c)
	if user == nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	var reseller *service.SignedInUser
	if !session.IsAdminUser(user) {
		var err error
		reseller, err = (&service.ResellerService{}).Me(user)
		if err != nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	}
	body, err := fs.ReadFile(distFS, "dist/openapi.json")
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "msg": "openapi.json not found"})
		return
	}
	if reseller != nil {
		body, err = withResellerOpenAPI(body, reseller.Permissions.Plans)
		if err != nil {
			logger.Warning("openapi.json: could not filter reseller paths:", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}

	// The embedded spec ships with `servers: [{url: "/"}]`. When the panel runs
	// under a non-root web base path, Swagger UI "Try it out" and external
	// generators must target that prefix, so rewrite the single server entry to
	// the runtime base path before serving.
	if basePath := c.GetString("base_path"); basePath != "" && basePath != "/" {
		if rebuilt, err := withServerBasePath(body, basePath); err != nil {
			logger.Warning("openapi.json: could not inject base path:", err)
		} else {
			body = rebuilt
		}
	}

	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func withResellerOpenAPI(spec []byte, allowPlans bool) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(spec, &doc); err != nil {
		return nil, err
	}
	var paths map[string]json.RawMessage
	if err := json.Unmarshal(doc["paths"], &paths); err != nil {
		return nil, err
	}
	if paths == nil {
		return nil, errors.New("OpenAPI paths must be an object")
	}
	for path := range paths {
		if !resellerOpenAPIPath(path, allowPlans) {
			delete(paths, path)
		}
	}
	filtered, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	doc["paths"] = filtered
	delete(doc, "x-websocket-messages")
	return json.Marshal(doc)
}

func resellerOpenAPIPath(path string, allowPlans bool) bool {
	switch path {
	case "/panel/api/me", "/panel/api/activity", "/panel/api/activity/read", "/panel/api/activity/alerts":
		return true
	}
	if !allowPlans && (path == "/panel/api/reseller/plans" || strings.HasPrefix(path, "/panel/api/reseller/plans/")) {
		return false
	}
	return strings.HasPrefix(path, "/panel/api/reseller/")
}

// withServerBasePath rewrites the spec's `servers` entry so requests target the
// panel's configured web base path. Only the top-level `servers` field is
// replaced; every other field is preserved verbatim via json.RawMessage.
func withServerBasePath(spec []byte, basePath string) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(spec, &doc); err != nil {
		return nil, err
	}
	servers, err := json.Marshal([]map[string]string{{
		"url":         strings.TrimSuffix(basePath, "/"),
		"description": "Current panel",
	}})
	if err != nil {
		return nil, err
	}
	doc["servers"] = servers
	return json.Marshal(doc)
}

func serveDistPage(c *gin.Context, name string) {
	body, err := fs.ReadFile(distFS, "dist/"+name)
	if err != nil {
		c.String(http.StatusInternalServerError, "missing embedded page: %s", name)
		return
	}

	basePath := c.GetString("base_path")
	if basePath == "" {
		basePath = "/"
	}

	if basePath != "/" {
		body = bytes.ReplaceAll(body, []byte(`src="/assets/`), []byte(`src="`+basePath+`assets/`))
		body = bytes.ReplaceAll(body, []byte(`href="/assets/`), []byte(`href="`+basePath+`assets/`))
	}

	escapedBase := jsStringEscape(basePath)
	csrfToken, err := session.EnsureCSRFToken(c)
	if err != nil {
		logger.Warning("Unable to mint CSRF token for", name+":", err)
		csrfToken = ""
	}
	csrfMeta := []byte(`<meta name="csrf-token" content="` + htmlpkg.EscapeString(csrfToken) + `">`)
	basePathMeta := []byte(`<meta name="base-path" content="` + htmlpkg.EscapeString(basePath) + `">`)

	nonceAttr := ""
	if nonce := c.GetString("csp_nonce"); nonce != "" {
		nonceAttr = ` nonce="` + htmlpkg.EscapeString(nonce) + `"`
	}
	script := `<script data-cfasync="false"` + nonceAttr + `>window.PUI_BASE_PATH="` + escapedBase + `"`
	if name != "login.html" {
		escapedVer := jsStringEscape(config.GetPanelVersion())
		script += `;window.PUI_CUR_VER="` + escapedVer + `"`
		// The shell's account menu needs the signed-in name on first paint; the
		// session already holds it, so no round trip earns its place here.
		if user := session.GetLoginUser(c); user != nil {
			script += `;window.PUI_USER="` + jsStringEscape(user.Username) + `"`
		}
	}
	script += `;</script>`
	inject := []byte(script)
	inject = append(inject, csrfMeta...)
	inject = append(inject, basePathMeta...)
	inject = append(inject, []byte(`</head>`)...)
	out := bytes.Replace(body, []byte("</head>"), inject, 1)

	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("Last-Modified", distPageBuildTime.UTC().Format(http.TimeFormat))
	c.Data(http.StatusOK, "text/html; charset=utf-8", out)
}

// jsStringEscape makes a value safe inside a JS string literal in an inline
// <script>. encoding/json's string encoder is the right tool and not a
// shortcut: it escapes the backslash, the quote and every control character,
// U+2028/U+2029, and writes `<`, `>` and `&` as JS unicode escapes -- the one
// thing the hand-written replacer here claimed and did not do. F210.
func jsStringEscape(s string) string {
	quoted, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(quoted[1 : len(quoted)-1])
}
