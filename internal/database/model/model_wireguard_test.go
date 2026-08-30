package model

import (
	"reflect"
	"testing"
)

func TestClientToRecordRoundTripWireGuard(t *testing.T) {
	c := &Client{
		Email:        "alice@example.test",
		Enable:       true,
		PrivateKey:   "cGVlci1wcml2YXRlLWtleS1iYXNlNjQtMzJieXRlcw==",
		PublicKey:    "cGVlci1wdWJsaWMta2V5LWJhc2U2NC0zMmJ5dGVzISE=",
		AllowedIPs:   []string{"10.0.0.2/32", "fd00::2/128"},
		PreSharedKey: PreSharedKeyOf("cHNrLWJhc2U2NC0zMmJ5dGVzLXBsYWNlaG9sZGVyISE="),
		KeepAlive:    25,
	}

	rec := c.ToRecord()
	if rec.AllowedIPs != "10.0.0.2/32,fd00::2/128" {
		t.Fatalf("AllowedIPs CSV = %q, want %q", rec.AllowedIPs, "10.0.0.2/32,fd00::2/128")
	}

	got := rec.ToClient()
	for _, f := range []struct {
		name string
		a, b any
	}{
		{"PrivateKey", c.PrivateKey, got.PrivateKey},
		{"PublicKey", c.PublicKey, got.PublicKey},
		// Compared by VALUE: the field is a *string so the panel can tell an
		// omitted key from a cleared one, and a round trip through the record
		// necessarily mints a new pointer.
		{"PreSharedKey", c.PSK(), got.PSK()},
		{"KeepAlive", c.KeepAlive, got.KeepAlive},
	} {
		if f.a != f.b {
			t.Errorf("%s round-trip = %v, want %v", f.name, f.b, f.a)
		}
	}
	if !reflect.DeepEqual(got.AllowedIPs, c.AllowedIPs) {
		t.Errorf("AllowedIPs round-trip = %v, want %v", got.AllowedIPs, c.AllowedIPs)
	}
}

func TestClientRecordEmptyAllowedIPs(t *testing.T) {
	rec := &ClientRecord{Email: "bob@example.test", AllowedIPs: ""}
	if got := rec.ToClient().AllowedIPs; got != nil {
		t.Fatalf("empty CSV → AllowedIPs = %v, want nil", got)
	}

	rec.AllowedIPs = " 10.0.0.5/32 , ,"
	if got := rec.ToClient().AllowedIPs; !reflect.DeepEqual(got, []string{"10.0.0.5/32"}) {
		t.Fatalf("trimmed CSV → AllowedIPs = %v, want [10.0.0.5/32]", got)
	}
}

func TestMergeClientRecordWireGuardKeysPreserved(t *testing.T) {
	existing := &ClientRecord{
		Email:      "carol@example.test",
		PrivateKey: "existing-private",
		PublicKey:  "existing-public",
		AllowedIPs: "10.0.0.7/32",
		UpdatedAt:  100,
	}
	incomingEmpty := &ClientRecord{Email: "carol@example.test", UpdatedAt: 200}
	MergeClientRecord(existing, incomingEmpty)
	if existing.PrivateKey != "existing-private" || existing.PublicKey != "existing-public" {
		t.Fatalf("empty incoming wiped keys: priv=%q pub=%q", existing.PrivateKey, existing.PublicKey)
	}
	if existing.AllowedIPs != "10.0.0.7/32" {
		t.Fatalf("empty incoming wiped allowedIPs: %q", existing.AllowedIPs)
	}

	incomingNewer := &ClientRecord{
		Email:      "carol@example.test",
		AllowedIPs: "10.0.0.8/32",
		UpdatedAt:  300,
	}
	MergeClientRecord(existing, incomingNewer)
	if existing.AllowedIPs != "10.0.0.8/32" {
		t.Fatalf("newer allowedIPs not applied: %q", existing.AllowedIPs)
	}
}

/*
A record with no key reads back as "not set", not as a deliberate clear.

The two are different values now — nil versus a pointer to empty — and only one
of them is a stored state. Getting this backwards would make every load of a
client with no pre-shared key look like a request to remove one. F179.
*/
func TestARecordWithNoPreSharedKeyReadsBackAsUnset(t *testing.T) {
	rec := &ClientRecord{Email: "carol@example.test"}
	client := rec.ToClient()

	if client.PreSharedKey != nil {
		t.Fatalf("a stored empty key came back as %v; that is the shape that means 'clear it'", *client.PreSharedKey)
	}
	if client.ClearsPreSharedKey() {
		t.Fatal("loading a client with no pre-shared key reads as a request to clear one")
	}

	set := &ClientRecord{Email: "dave@example.test", PreSharedKey: "a-key"}
	if got := set.ToClient().PSK(); got != "a-key" {
		t.Fatalf("stored key came back as %q", got)
	}
}

// And the wire distinction the whole change exists for.
func TestClearsPreSharedKeyOnlyForADeliberateEmpty(t *testing.T) {
	empty := ""
	value := "a-key"
	for _, tc := range []struct {
		name  string
		field *string
		clear bool
	}{
		{name: "omitted", field: nil},
		{name: "sent empty", field: &empty, clear: true},
		{name: "sent a value", field: &value},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Client{PreSharedKey: tc.field}).ClearsPreSharedKey(); got != tc.clear {
				t.Fatalf("ClearsPreSharedKey() = %v, want %v", got, tc.clear)
			}
		})
	}
}
