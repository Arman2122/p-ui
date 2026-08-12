package shaping

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
)

// ProbeDevice is the scratch mirror preflight builds its whole tree on. The name
// is outside every owned namespace on purpose, so no GC pass can race it away.
const ProbeDevice = "pui-shape0"

// probePrefix is a documentation address (RFC 5737). It never leaves the probe
// device, which is deleted before anything is routed to it.
var probePrefix = netip.MustParsePrefix("192.0.2.1/32")

// Report is what preflight found. A refusal disables shaping and names the
// missing piece; a note is a host fact the operator has to know about.
type Report struct {
	Refusals []error
	Notes    []string
}

// Err joins the refusals so a caller can still match the sentinel that names the
// remedy — errors.Is over a flattened string would lose exactly that.
func (r Report) Err() error { return errors.Join(r.Refusals...) }

func (r Report) OK() bool { return len(r.Refusals) == 0 }

/*
Preflight builds the entire mechanism on a scratch device and tears it down again.

It asks by doing rather than by reading /proc/modules, because a module can be
present and still refuse the object this panel installs, and because a listing is
an unprivileged read that says nothing about CAP_NET_ADMIN. Every failure names
the module an operator would have to load, and none of them stops the panel:
Core.Preflight's rule is that a failure disables a capability.
*/
func (m *Manager) Preflight(ctx context.Context) Report {
	var report Report
	if err := m.plane.Probe(ctx); err != nil {
		report.Refusals = append(report.Refusals, err)
		return report
	}

	// A leftover from a probe that died mid-way would answer EEXIST to every step
	// below and read as a broken kernel.
	if err := m.plane.DeleteIFB(ctx, ProbeDevice); err != nil && !settledDel(err) {
		report.Notes = append(report.Notes, fmt.Sprintf("a previous shaping probe left %s behind and it could not be removed: %v", ProbeDevice, err))
	}
	if err := m.plane.EnsureIFB(ctx, ProbeDevice); err != nil {
		report.Refusals = append(report.Refusals, fmt.Errorf("%w: ifb — the upload mirror cannot be created: %w", ErrModuleMissing, err))
		return report
	}
	defer func() {
		if err := m.plane.DeleteIFB(ctx, ProbeDevice); err != nil && !settledDel(err) {
			report.Notes = append(report.Notes, fmt.Sprintf("the shaping probe device %s could not be removed: %v", ProbeDevice, err))
		}
	}()

	root := rootQdisc(ProbeDevice)
	class := ClassSpec{
		Device: ProbeDevice, Handle: classHandle(firstMinor), Parent: rootHandle,
		RateBytesPerSec: KernelBytesPerSec(UnlimitedBps), CeilBytesPerSec: KernelBytesPerSec(UnlimitedBps),
	}
	steps := []struct {
		module string
		run    func() error
	}{
		{"sch_htb", func() error { return m.plane.AddQdisc(ctx, root) }},
		{"sch_htb", func() error { return m.plane.AddClass(ctx, class) }},
		{"sch_sfq", func() error {
			return m.plane.AddQdisc(ctx, QdiscSpec{Device: ProbeDevice, Type: QdiscSfq, Parent: class.Handle})
		}},
		{"cls_flower", func() error {
			return m.plane.AddFilter(ctx, FilterSpec{
				Device: ProbeDevice, Parent: rootHandle, Priority: ourPriority(probePrefix),
				Match: MatchDst, Prefix: probePrefix, ClassID: class.Handle,
			})
		}},
		{"clsact", func() error { return m.plane.AddQdisc(ctx, clsactQdisc(ProbeDevice)) }},
		{"act_mirred", func() error {
			return m.plane.AddFilter(ctx, FilterSpec{
				Device: ProbeDevice, Parent: ingressBlock, Priority: ourPriority(probePrefix),
				Match: MatchSrc, Prefix: probePrefix, Redirect: ProbeDevice,
			})
		}},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			report.Refusals = append(report.Refusals, fmt.Errorf("%w: %s — %w", ErrModuleMissing, step.module, err))
			return report
		}
	}
	return report
}
