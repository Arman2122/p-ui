package locale

import (
	"encoding/json"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

/*
A key the requested locale lacks renders in the default language, not as "".

go-i18n's Localize returns the default-language text TOGETHER with a
MessageNotFoundErr when only the fallback carries the key. localize discarded
the text on any error, so a Persian request for a key that only en-US had --
every key added in one locale first -- rendered as nothing at all. F406.
*/
func TestLocalizeFallsBackToTheDefaultLanguageText(t *testing.T) {
	i18nBundle = i18n.NewBundle(language.MustParse("en-US"))
	i18nBundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	mustAddMessages(t, "en-US", `{"greeting":"hello","only_english":"English only"}`)
	mustAddMessages(t, "fa-IR", `{"greeting":"salaam"}`)

	fa := i18n.NewLocalizer(i18nBundle, "fa-IR")
	if got := localize(fa, "greeting"); got != "salaam" {
		t.Fatalf("a translated key = %q, want salaam", got)
	}
	if got := localize(fa, "only_english"); got != "English only" {
		t.Fatalf("a key only en-US carries = %q, want the English text, not an empty string", got)
	}
}
