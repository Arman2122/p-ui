/*
Package banset is the panel-owned firewall ban set: the mechanism by which a
device over its cap is dropped on the ports it was capped on, for as long as the
panel says. It deals in addresses, ports and deadlines. Who was banned, on which
inbound, and why is the panel's, and this package never learns it.

The set lives in the panel's own nft table, in the pattern the NAT table already
follows, so applying or deleting it whole can never disturb another owner's
rules. Every element carries its deadline as a kernel timeout, so a panel that
dies mid-ban still releases it on time. The drop is terminal for every packet
of a flow already open, measured, so a ban lands on the connection the device
holds and not only on its next one.
*/
package banset

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Table is the panel's own nft table for bans.
const Table = "p-ui-ipl"

var (
	ErrPlatformUnsupported = errors.New("banset: firewall bans are available on Linux only")
	ErrNoNft               = errors.New("banset: nft is not installed, so a device over its cap cannot be dropped")
	ErrPermission          = errors.New("banset: writing the firewall needs root")
)

// Entry is one banned source on one port until a deadline.
type Entry struct {
	Source netip.Addr
	Port   uint16
	Until  time.Time
}

/*
Ruleset is the whole table, deleted and rewritten in one atomic apply, carrying
only the entries still live at now. Rewritten rather than flushed so nothing a
previous panel life left in the sets survives, and deleted then defined in ONE
transaction so there is no instant without a table.

An entry past its deadline is left out rather than rendered: an element with a
zero timeout is a parse error, nft -f then rejects the transaction whole, and
every ban on the host would go with it.
*/
func Ruleset(now time.Time, entries []Entry) string {
	return render(now, liveSorted(now, entries))
}

func render(now time.Time, live []Entry) string {
	var v4, v6 []string
	for _, e := range live {
		secs := int64(math.Ceil(e.Until.Sub(now).Seconds()))
		if secs < 1 {
			secs = 1
		}
		source := e.Source.Unmap()
		element := fmt.Sprintf("%s . %d timeout %ds", source, e.Port, secs)
		if source.Is4() {
			v4 = append(v4, element)
		} else {
			v6 = append(v6, element)
		}
	}

	var b strings.Builder
	b.WriteString(teardown())
	b.WriteString("table inet " + Table + " {\n")
	writeSet(&b, "v4", "ipv4_addr", v4)
	writeSet(&b, "v6", "ipv6_addr", v6)
	b.WriteString("\tchain input {\n")
	// A numeric priority: `filter - 1` is a spelling older nft may not read.
	b.WriteString("\t\ttype filter hook input priority -1; policy accept;\n")
	b.WriteString("\t\tip saddr . tcp dport @v4 drop\n")
	b.WriteString("\t\tip saddr . udp dport @v4 drop\n")
	b.WriteString("\t\tip6 saddr . tcp dport @v6 drop\n")
	b.WriteString("\t\tip6 saddr . udp dport @v6 drop\n")
	b.WriteString("\t}\n}\n")
	return b.String()
}

func writeSet(b *strings.Builder, name, addrType string, elements []string) {
	fmt.Fprintf(b, "\tset %s {\n\t\ttype %s . inet_service\n\t\tflags timeout\n", name, addrType)
	if len(elements) > 0 {
		fmt.Fprintf(b, "\t\telements = { %s }\n", strings.Join(elements, ", "))
	}
	b.WriteString("\t}\n")
}

// teardown removes the table without `destroy`, which nft learned in 1.0.8:
// `add` is a no-op on a table that exists, so the `delete` always has a target.
func teardown() string {
	return "add table inet " + Table + "\ndelete table inet " + Table + "\n"
}

func liveSorted(now time.Time, entries []Entry) []Entry {
	live := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Source.IsValid() && e.Port != 0 && e.Until.After(now) {
			live = append(live, e)
		}
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].Source != live[j].Source {
			return live[i].Source.Less(live[j].Source)
		}
		return live[i].Port < live[j].Port
	})
	return live
}

// fingerprint is the want itself: sources, ports and deadlines. Not the rendered
// script, whose timeouts count down every pass and would never read as unchanged.
func fingerprint(live []Entry) string {
	var b strings.Builder
	for _, e := range live {
		b.WriteString(e.Source.Unmap().String())
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(int(e.Port)))
		b.WriteByte('@')
		b.WriteString(strconv.FormatInt(e.Until.Unix(), 10))
		b.WriteByte('\n')
	}
	return b.String()
}

/*
Manager converges the host's ban set on what the panel wants, level-triggered:
handed the whole wanted set every pass, it writes the firewall only when the set
changed, and a write that failed leaves the want unrecorded so the next pass
tries again.
*/
type Manager struct {
	plane Plane

	mu sync.Mutex
	// applied is the fingerprint of the last set written; primed is false
	// before the first write, so an empty first want still clears the table.
	applied string
	primed  bool
}

func NewManager(p Plane) *Manager { return &Manager{plane: p} }

// HostManager converges this host's own firewall.
func HostManager() *Manager { return NewManager(hostPlane()) }

// Probe reports whether this host's firewall can be written by this panel at all.
func (m *Manager) Probe(ctx context.Context) error { return m.plane.Probe(ctx) }

func (m *Manager) Converge(ctx context.Context, now time.Time, entries []Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	live := liveSorted(now, entries)
	want := fingerprint(live)
	if m.primed && want == m.applied {
		return nil
	}
	script := teardown()
	if len(live) > 0 {
		script = render(now, live)
	}
	if err := m.plane.Apply(ctx, script); err != nil {
		return err
	}
	m.applied, m.primed = want, true
	return nil
}
