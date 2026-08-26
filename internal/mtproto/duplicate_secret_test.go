package mtproto

import (
	"strings"
	"testing"
)

/*
Two clients sharing a name must not stop the sidecar from starting.

The [secrets] section keys on the client email, and renderConfig wrote one line
per entry with no de-duplication. Two entries with the same name produce the same
TOML key twice, which any conforming parser refuses — so the process exits at
startup and the inbound serves nobody.

The hot-apply path never had the problem: secretsPayload builds a map, so the
second entry silently replaces the first. That is the real defect. Editing a
client worked and restarting the panel did not, and the two paths disagreed
about the same inbound. F220.

The CRUD paths do refuse a duplicate email, so this is data that arrived another
way — an older row, a direct API write, a race between two adds. A renderer must
not be able to emit a config the core cannot load, whatever put the data there.
*/

func duplicateSecretInstance() Instance {
	return Instance{
		Id:   1,
		Port: 8443,
		Secrets: []SecretEntry{
			{Name: "shared@e", Secret: "aaaa", AdTag: "tag-one", QuotaBytes: 1000},
			{Name: "unique@e", Secret: "bbbb"},
			{Name: "shared@e", Secret: "cccc", AdTag: "tag-two", QuotaBytes: 2000},
		},
	}
}

func TestRenderConfigWritesEachNameOnce(t *testing.T) {
	config := renderConfig(duplicateSecretInstance(), 7000, "")

	for _, section := range []struct{ header, key string }{
		{"[secrets]", `"shared@e" = `},
		{"[secret-ad-tags]", `"shared@e" = `},
	} {
		start := strings.Index(config, section.header)
		if start < 0 {
			t.Fatalf("no %s section rendered:\n%s", section.header, config)
		}
		body := config[start:]
		if end := strings.Index(body[1:], "\n["); end >= 0 {
			body = body[:end+1]
		}
		if n := strings.Count(body, section.key); n != 1 {
			t.Errorf("%s repeats the key %s %d times; mtg refuses a config with a duplicate TOML key and the inbound never comes up:\n%s",
				section.header, section.key, n, body)
		}
	}

	// [secret-limits.<name>] is a table header, so a repeat is the same error.
	if n := strings.Count(config, `[secret-limits."shared@e"]`); n != 1 {
		t.Errorf("[secret-limits] repeats a table header %d times:\n%s", n, config)
	}
}

// And it collapses the way the hot-apply path does, so a restart and an edit
// leave the sidecar serving the same secret rather than two different ones.
func TestRenderConfigAgreesWithTheHotApplyPath(t *testing.T) {
	inst := duplicateSecretInstance()
	config := renderConfig(inst, 7000, "")
	payload := secretsPayload(inst)

	applied, ok := payload.Secrets["shared@e"]
	if !ok {
		t.Fatal("the hot-apply payload lost the duplicated name entirely")
	}
	if !strings.Contains(config, `"shared@e" = `+`"`+applied.Secret+`"`) {
		t.Fatalf("the rendered config and the hot-apply payload disagree about which secret survives: payload has %q\n%s",
			applied.Secret, config)
	}
	if !strings.Contains(config, `"unique@e" = "bbbb"`) {
		t.Fatalf("de-duplication dropped an entry that was never duplicated:\n%s", config)
	}
}
