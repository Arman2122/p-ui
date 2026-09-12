package locale

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

func TestFrontendReservedLabelsDoNotBreakBackendTranslations(t *testing.T) {
	for _, code := range []string{"en-US", "fa-IR"} {
		t.Run(code, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "translation", code+".json"))
			if err != nil {
				t.Fatal(err)
			}
			flattened, err := flattenTranslationJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			bundle := i18n.NewBundle(language.English)
			bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
			if _, err := bundle.ParseMessageFileBytes(flattened, code+".json"); err != nil {
				t.Fatal(err)
			}
			localizer := i18n.NewLocalizer(bundle, code)
			var original map[string]any
			if err := json.Unmarshal(data, &original); err != nil {
				t.Fatal(err)
			}
			want := original["plans"].(map[string]any)["description"].(string)
			got, err := localizer.Localize(&i18n.LocalizeConfig{MessageID: "plans.description"})
			if err != nil || got != want {
				t.Fatalf("label = %q, err=%v; want %q", got, err, want)
			}
			got, err = localizer.Localize(&i18n.LocalizeConfig{MessageID: "username"})
			if err != nil || got != original["username"] {
				t.Fatalf("legacy label = %q, err=%v", got, err)
			}
		})
	}
}
