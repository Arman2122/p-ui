package awg

import (
	"fmt"
	"strconv"
	"strings"
)

/*
Ranges as awg-tools writes them: "lo-hi", or a bare number when the bounds match.

The panel stores a header as the u64 the kernel wants, (hi<<32 | lo), and a timer
as the u32 (hi<<16 | lo). An operator types neither. This is the one translation
between the two, so a form field can take what a person would write in a .conf
and a stored value can be shown back the same way.

Why it is not optional: written straight through as a number, "10" becomes the
range [10, 0] -- an upper bound below its lower bound, which nothing can
intersect and which the kernel's own overlap check therefore never fires on. Two
message types can then share a value and the far end cannot tell a handshake
from a transport packet.
*/

// ParseHeaderRange reads "lo-hi" or "n" into the u64 a header attribute takes.
func ParseHeaderRange(text string) (uint64, error) {
	lo, hi, err := parseBounds(text, 32)
	if err != nil {
		return 0, err
	}
	return HeaderRange(uint32(lo), uint32(hi)), nil
}

// ParseTimerRange reads "lo-hi" or "n" into the u32 a timer attribute takes.
func ParseTimerRange(text string) (uint32, error) {
	lo, hi, err := parseBounds(text, 16)
	if err != nil {
		return 0, err
	}
	return TimerRange(uint16(lo), uint16(hi)), nil
}

// FormatHeaderRange writes a stored header back the way it was typed.
func FormatHeaderRange(value uint64) string {
	return formatBounds(uint64(uint32(value)), uint64(uint32(value>>32)))
}

// FormatTimerRange writes a stored timer back the way it was typed.
func FormatTimerRange(value uint32) string {
	return formatBounds(uint64(uint16(value)), uint64(uint16(value>>16)))
}

func parseBounds(text string, bits int) (uint64, uint64, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, 0, nil
	}
	loText, hiText, split := strings.Cut(trimmed, "-")
	lo, err := strconv.ParseUint(strings.TrimSpace(loText), 10, bits)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %q", ErrRangeText, text)
	}
	if !split {
		// A bare number is the range [n, n], which is how awg-tools reads it too.
		return lo, lo, nil
	}
	hi, err := strconv.ParseUint(strings.TrimSpace(hiText), 10, bits)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %q", ErrRangeText, text)
	}
	if hi < lo {
		// Refused rather than swapped: the kernel would take it as an empty range
		// and quietly stop enforcing anything that depends on it.
		return 0, 0, fmt.Errorf("%w: %q has an upper bound below its lower one", ErrRangeText, text)
	}
	return lo, hi, nil
}

func formatBounds(lo, hi uint64) string {
	if lo == 0 && hi == 0 {
		return ""
	}
	if lo == hi {
		return strconv.FormatUint(lo, 10)
	}
	return fmt.Sprintf("%d-%d", lo, hi)
}
