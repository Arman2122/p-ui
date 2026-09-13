package sub

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The Go half of the share-link golden corpus.

Share links are built more than once in this repo and nothing enforced that the
copies agree, which is the single cause behind fourteen of this campaign's
findings — always the same sentence with different nouns: renderer A emits a
field renderer B omits, so the panel shows one tunnel and the subscription hands
out another. The corpus states the parameters once; each renderer has a harness
that must reproduce them.

testdata/sharelink/README.md carries the fixture shape and the reasoning. The
frontend half is frontend/src/test/sharelink-corpus.test.ts, and the two must
stay pointed at the same directory: a fixture added for one renderer is a
fixture the other is now held to.
*/

type goldenFixture struct {
	Name    string `json:"name"`
	Why     string `json:"why"`
	Email   string `json:"email"`
	Inbound struct {
		Protocol       string `json:"protocol"`
		Listen         string `json:"listen"`
		Port           int    `json:"port"`
		Remark         string `json:"remark"`
		Settings       string `json:"settings"`
		StreamSettings string `json:"streamSettings"`
		Sniffing       string `json:"sniffing"`
	} `json:"inbound"`
	Expected struct {
		Scheme   string            `json:"scheme"`
		Userinfo string            `json:"userinfo"`
		Host     string            `json:"host"`
		Port     int               `json:"port"`
		Params   map[string]string `json:"params"`
	} `json:"expected"`
	PresentOnly []string `json:"presentOnly"`
	// AbsentParams is the other half of the contract: a field the renderer must
	// NOT emit. Named separately because Go reads an absent param as "" and the
	// frontend reads it as null, so no declared value expresses it on both.
	AbsentParams []string `json:"absentParams"`
}

func loadGoldenCorpus(t *testing.T) []goldenFixture {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "sharelink")
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob corpus: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures in %s; the corpus is the test, so an empty one is a failure, not a pass", dir)
	}
	out := make([]goldenFixture, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var fixture goldenFixture
		if err := json.Unmarshal(body, &fixture); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if fixture.Name != strings.TrimSuffix(filepath.Base(path), ".json") {
			t.Fatalf("%s declares name %q; the two must match so a failure names the file", path, fixture.Name)
		}
		out = append(out, fixture)
	}
	return out
}

func TestEveryCorpusFixtureRendersItsDeclaredLink(t *testing.T) {
	for _, fixture := range loadGoldenCorpus(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			raw := (&SubService{}).GetLink(&model.Inbound{
				Protocol:       model.Protocol(fixture.Inbound.Protocol),
				Listen:         fixture.Inbound.Listen,
				Port:           fixture.Inbound.Port,
				Remark:         fixture.Inbound.Remark,
				Settings:       fixture.Inbound.Settings,
				StreamSettings: fixture.Inbound.StreamSettings,
			}, fixture.Email)
			if raw == "" {
				t.Fatalf("rendered no link at all (%s)", fixture.Why)
			}
			// A fixture declares one endpoint; a multi-line render is a
			// different fixture's job.
			if first, _, cut := strings.Cut(raw, "\n"); cut {
				raw = first
			}

			view := linkViewOf(t, strings.TrimSpace(raw))
			if view.scheme != fixture.Expected.Scheme {
				t.Errorf("scheme = %q, want %q", view.scheme, fixture.Expected.Scheme)
			}
			if view.user != fixture.Expected.Userinfo {
				t.Errorf("userinfo = %q, want %q", view.user, fixture.Expected.Userinfo)
			}
			wantHost := fixture.Expected.Host + ":" + strconv.Itoa(fixture.Expected.Port)
			if view.host != wantHost {
				t.Errorf("host = %q, want %q", view.host, wantHost)
			}

			query := view.query
			for key, want := range fixture.Expected.Params {
				if got := query.Get(key); got != want {
					t.Errorf("param %q = %q, want %q — %s", key, got, want, fixture.Why)
				}
			}
			for _, key := range fixture.PresentOnly {
				if !query.Has(key) {
					t.Errorf("param %q is absent; the fixture requires it to be emitted", key)
				}
			}
			for _, key := range fixture.AbsentParams {
				if query.Has(key) {
					t.Errorf("param %q = %q was emitted; the fixture requires it to be absent — %s",
						key, query.Get(key), fixture.Why)
				}
			}
		})
	}
}

// linkView is one rendered link as the fixtures describe it: a URI's parts, or
// a vmess:// document's keys read as parameters, so one fixture shape covers both.
type linkView struct {
	scheme, user, host string
	query              url.Values
}

func linkViewOf(t *testing.T, raw string) linkView {
	t.Helper()
	if payload, ok := strings.CutPrefix(raw, "vmess://"); ok {
		body, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			body, err = base64.RawStdEncoding.DecodeString(payload)
		}
		if err != nil {
			t.Fatalf("vmess payload is not base64: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("vmess payload is not a JSON object: %v\n%s", err, body)
		}
		view := linkView{scheme: "vmess", query: url.Values{}}
		for key, value := range doc {
			switch v := value.(type) {
			case string:
				view.query.Set(key, v)
			case float64:
				view.query.Set(key, strconv.FormatFloat(v, 'f', -1, 64))
			case bool:
				view.query.Set(key, strconv.FormatBool(v))
			}
		}
		view.host = view.query.Get("add") + ":" + view.query.Get("port")
		return view
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("rendered a link that does not parse: %v\n got: %s", err, raw)
	}
	return linkView{scheme: parsed.Scheme, user: parsed.User.Username(), host: parsed.Host, query: parsed.Query()}
}
