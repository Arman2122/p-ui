package banset

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type recordingPlane struct {
	scripts []string
	fail    error
}

func (p *recordingPlane) Probe(context.Context) error { return nil }

func (p *recordingPlane) Apply(_ context.Context, script string) error {
	if p.fail != nil {
		return p.fail
	}
	p.scripts = append(p.scripts, script)
	return nil
}

var t0 = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func entry(source string, port uint16, until time.Time) Entry {
	return Entry{Source: netip.MustParseAddr(source), Port: port, Until: until}
}

func converge(t *testing.T, m *Manager, now time.Time, want []Entry) {
	t.Helper()
	if err := m.Converge(t.Context(), now, want); err != nil {
		t.Fatalf("converge: %v", err)
	}
}

/*
The whole table in one transaction, only the live entries in it, sources sorted
so the same want renders the same text. An entry past its deadline is absent,
not rendered with a zero timeout that nft would reject along with every other
ban; one inside its last second rounds up to the one second nft can express.
*/
func TestTheRulesetIsOneAtomicReplaceOfOnlyTheLiveEntries(t *testing.T) {
	got := Ruleset(t0, []Entry{
		entry("9.9.9.9", 53, t0.Add(400*time.Millisecond)),
		entry("1.2.3.4", 443, t0.Add(30*time.Minute)),
		entry("2001:db8::1", 8443, t0.Add(time.Hour)),
		entry("5.6.7.8", 80, t0.Add(-time.Second)),
	})
	want := "add table inet p-ui-ipl\n" +
		"delete table inet p-ui-ipl\n" +
		"table inet p-ui-ipl {\n" +
		"\tset v4 {\n" +
		"\t\ttype ipv4_addr . inet_service\n" +
		"\t\tflags timeout\n" +
		"\t\telements = { 1.2.3.4 . 443 timeout 1800s, 9.9.9.9 . 53 timeout 1s }\n" +
		"\t}\n" +
		"\tset v6 {\n" +
		"\t\ttype ipv6_addr . inet_service\n" +
		"\t\tflags timeout\n" +
		"\t\telements = { 2001:db8::1 . 8443 timeout 3600s }\n" +
		"\t}\n" +
		"\tchain input {\n" +
		"\t\ttype filter hook input priority -1; policy accept;\n" +
		"\t\tip saddr . tcp dport @v4 drop\n" +
		"\t\tip saddr . udp dport @v4 drop\n" +
		"\t\tip6 saddr . tcp dport @v6 drop\n" +
		"\t\tip6 saddr . udp dport @v6 drop\n" +
		"\t}\n" +
		"}\n"
	if got != want {
		t.Fatalf("ruleset:\n%s\nwant:\n%s", got, want)
	}
}

// The want is sources, ports and deadlines. The timeouts counting down between
// two passes are not a change, and a moved deadline is.
func TestAnUnchangedWantIsNotRewritten(t *testing.T) {
	p := &recordingPlane{}
	m := NewManager(p)
	want := []Entry{entry("1.2.3.4", 443, t0.Add(30*time.Minute))}

	converge(t, m, t0, want)
	converge(t, m, t0.Add(10*time.Second), want)
	if len(p.scripts) != 1 {
		t.Fatalf("the firewall was written %d times for one unchanged want", len(p.scripts))
	}

	converge(t, m, t0.Add(20*time.Second), []Entry{entry("1.2.3.4", 443, t0.Add(time.Hour))})
	if len(p.scripts) != 2 {
		t.Fatalf("a moved deadline was not written (%d writes)", len(p.scripts))
	}
	if !strings.Contains(p.scripts[1], "1.2.3.4 . 443 timeout 3580s") {
		t.Fatalf("the moved deadline was written as:\n%s", p.scripts[1])
	}
}

// The kernel expires an element on its own; the next write must agree with it,
// and must never render the expired entry back in.
func TestAnEntryPastItsDeadlineLeavesTheNextWrite(t *testing.T) {
	p := &recordingPlane{}
	m := NewManager(p)
	want := []Entry{
		entry("1.2.3.4", 443, t0.Add(time.Minute)),
		entry("5.6.7.8", 80, t0.Add(time.Hour)),
	}

	converge(t, m, t0, want)
	converge(t, m, t0.Add(2*time.Minute), want)
	if len(p.scripts) != 2 {
		t.Fatalf("an entry expiring is a change to the want; got %d writes", len(p.scripts))
	}
	if strings.Contains(p.scripts[1], "1.2.3.4") {
		t.Fatalf("an expired entry was rendered; nft would reject the transaction and every ban with it:\n%s", p.scripts[1])
	}
	if !strings.Contains(p.scripts[1], "5.6.7.8 . 80 timeout 3480s") {
		t.Fatalf("the live entry lost its deadline:\n%s", p.scripts[1])
	}
}

// The last ban leaving takes the table with it, and an empty first want still
// clears whatever a previous panel life left behind, once.
func TestNothingWantedTearsTheTableDown(t *testing.T) {
	p := &recordingPlane{}
	m := NewManager(p)

	converge(t, m, t0, nil)
	converge(t, m, t0.Add(10*time.Second), nil)
	if len(p.scripts) != 1 || p.scripts[0] != teardown() {
		t.Fatalf("an empty want should clear the table once; wrote %q", p.scripts)
	}

	converge(t, m, t0, []Entry{entry("1.2.3.4", 443, t0.Add(time.Hour))})
	converge(t, m, t0.Add(time.Second), nil)
	if len(p.scripts) != 3 || p.scripts[2] != teardown() {
		t.Fatalf("the last ban leaving did not take the table with it; wrote %d scripts", len(p.scripts))
	}
}

// Level-triggered: a write that failed is not remembered as done.
func TestAFailedWriteIsRetriedNextPass(t *testing.T) {
	p := &recordingPlane{fail: errors.New("nft: boom")}
	m := NewManager(p)
	want := []Entry{entry("1.2.3.4", 443, t0.Add(time.Hour))}

	if err := m.Converge(t.Context(), t0, want); err == nil {
		t.Fatal("a failed write reported success")
	}
	p.fail = nil
	converge(t, m, t0.Add(10*time.Second), want)
	if len(p.scripts) != 1 {
		t.Fatalf("the want was not written once the failure cleared (%d writes)", len(p.scripts))
	}
}

// Two clients behind one address, both banned on one port, are one element to
// the kernel, and it keeps the first deadline it reads (measured, nft 1.0.9).
func TestOneElementPerSourceAndPortWithTheLatestDeadline(t *testing.T) {
	got := Ruleset(t0, []Entry{
		entry("1.2.3.4", 443, t0.Add(10*time.Minute)),
		entry("1.2.3.4", 443, t0.Add(30*time.Minute)),
	})
	if strings.Count(got, "1.2.3.4 . 443") != 1 || !strings.Contains(got, "1.2.3.4 . 443 timeout 1800s") {
		t.Fatalf("want one element carrying the later deadline:\n%s", got)
	}
}

// A zone is a host-local detail nft does not read: rendered, it would have the
// whole transaction rejected and every ban on the host gone with it.
func TestAZonedSourceRendersWithoutItsZone(t *testing.T) {
	got := Ruleset(t0, []Entry{entry("fe80::1%eth0", 443, t0.Add(time.Hour))})
	if !strings.Contains(got, "fe80::1 . 443 timeout 3600s") || strings.Contains(got, "%eth0") {
		t.Fatalf("a zoned source must render as its bare address:\n%s", got)
	}
}
