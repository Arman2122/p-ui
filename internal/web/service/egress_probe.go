package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/web/service/outbound"
)

// The refusals a probe answers with instead of a number. Each one is a state
// where a latency figure would be a plausible lie rather than a measurement.
var (
	ErrEgressProbeDisabled  = errors.New("egress: a disabled egress carries nothing to measure")
	ErrEgressProbeContained = errors.New("egress: the egress is contained, so nothing leaves through it")
	ErrEgressProbeIsFront   = errors.New("egress: a front is measured by testing the outbound it targets")
)

/*
TestEgresses times a real request out through each egress and reports the exit
it came from, so an uplink's row answers the same question an outbound's does.

The whole reason this is not a thin wrapper around a dialer: a socket carrying a
mark no rule catches does not fail. It falls through to the main table and
leaves with the host's own address, and the probe then reports the server's
direct latency and IP under the uplink's name — the row looks healthiest exactly
when it is most broken. So every probe is gated on the host agreeing that this
mark reaches this egress's own device, read fresh rather than assumed from the
row.
*/
func (s *EgressService) TestEgresses(ctx context.Context, ids []int, mode string) ([]*outbound.TestOutboundResult, error) {
	if len(ids) == 0 {
		return []*outbound.TestOutboundResult{}, nil
	}
	var rows []*model.Egress
	if err := database.GetDB().Model(&model.Egress{}).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[int]*model.Egress, len(rows))
	for _, row := range rows {
		byID[row.Id] = row
	}

	results := make([]*outbound.TestOutboundResult, len(ids))
	for i, id := range ids {
		results[i] = s.probeOne(ctx, byID[id], id, mode)
	}
	return results, nil
}

func (s *EgressService) probeOne(ctx context.Context, row *model.Egress, id int, mode string) *outbound.TestOutboundResult {
	result := &outbound.TestOutboundResult{Tag: fmt.Sprintf("egress-%d", id), Mode: outbound.MarkedProbeMode(mode)}
	if row == nil {
		result.Error = fmt.Sprintf("egress %d no longer exists", id)
		return result
	}
	result.Tag = egressProbeTag(row)

	switch {
	case !row.Enable:
		result.Error = ErrEgressProbeDisabled.Error()
		return result
	// A front hands its traffic to an outbound tag, and that outbound is already
	// testable on its own row. Probing here would measure it a second time under
	// a second name.
	case egressFronts(row.Type):
		result.Error = fmt.Sprintf("%s: %q", ErrEgressProbeIsFront, row.Target)
		return result
	}

	routed, err := egressManager.MarkedExit(ctx, id)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if !routed.Routed() {
		result.Error = ErrEgressProbeContained.Error()
		return result
	}

	// Pinned to the families the egress carries: a v4-only uplink left to Go's
	// happy-eyeballs is probed over its own v6 blackhole and reports a working
	// exit as "invalid argument".
	outbound.ProbeMarked(egress.Mark(id), routed.Network(), mode, result)
	return result
}

// egressProbeTag labels the result with what the operator named the row, since
// an id alone is not what they are looking at in the table.
func egressProbeTag(row *model.Egress) string {
	if row.Remark != "" {
		return row.Remark
	}
	return fmt.Sprintf("egress-%d", row.Id)
}
