package awg

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

/*
An operator types "10-19", the kernel wants (hi<<32 | lo), and getting the
translation wrong is silent.

A header written straight through as the number 10 decodes as the range [10, 0]:
an upper bound below its lower one, which nothing can intersect, so the module's
own overlap check never fires. Two message types can then be configured onto the
same value and the far end cannot tell a handshake initiation from a transport
packet -- with nothing on either side reporting anything.
*/
func TestHeaderRangeTextRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		text string
		want uint64
	}{
		{"10-19", HeaderRange(10, 19)},
		// A bare number is [n, n], which is how awg-tools reads it too -- NOT the
		// bare value, which would be [n, 0].
		{"20", HeaderRange(20, 20)},
		{" 30 - 39 ", HeaderRange(30, 39)},
		{"", 0},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got, err := ParseHeaderRange(tc.text)
			if err != nil {
				t.Fatalf("ParseHeaderRange(%q): %v", tc.text, err)
			}
			if got != tc.want {
				t.Fatalf("ParseHeaderRange(%q) = %d, want %d", tc.text, got, tc.want)
			}
			if tc.text == "" {
				return
			}
			// What is shown back must parse to the same thing, or an edit that
			// touches nothing still rewrites the device.
			again, err := ParseHeaderRange(FormatHeaderRange(got))
			if err != nil || again != got {
				t.Fatalf("round trip through %q gave %d (%v), want %d", FormatHeaderRange(got), again, err, got)
			}
		})
	}
}

func TestTimerRangeTextRoundTrips(t *testing.T) {
	got, err := ParseTimerRange("100-140")
	if err != nil {
		t.Fatalf("ParseTimerRange: %v", err)
	}
	if got != TimerRange(100, 140) {
		t.Fatalf("ParseTimerRange = %d, want %d", got, TimerRange(100, 140))
	}
	if text := FormatTimerRange(got); text != "100-140" {
		t.Fatalf("FormatTimerRange = %q, want 100-140", text)
	}
	if text := FormatTimerRange(TimerRange(25, 25)); text != "25" {
		t.Fatalf("equal bounds must collapse to a bare number, got %q", text)
	}
}

/*
An inverted range is refused rather than swapped.

Swapping would be helpful right up until it silently changed what the operator
asked for; taken literally the kernel reads it as empty and stops enforcing
whatever depended on it, which is worse. Saying so is the only honest option.
*/
func TestAnInvertedRangeIsRefused(t *testing.T) {
	for _, text := range []string{"19-10", "not-a-range", "10-", "-", "99999999999999"} {
		if _, err := ParseHeaderRange(text); !errors.Is(err, ErrRangeText) {
			t.Errorf("ParseHeaderRange(%q) = %v, want ErrRangeText", text, err)
		}
	}
	// A value past a u16 is out of range for a timer even though a header takes it.
	if _, err := ParseTimerRange("70000"); !errors.Is(err, ErrRangeText) {
		t.Errorf("ParseTimerRange(70000) = %v, want ErrRangeText", err)
	}
}

/*
Params must survive the trip through JSON with its ranges intact.

The form sends "10-19", older settings carry a plain number, and both have to
land on the same packed value the kernel wants -- because a header stored one
way and read the other is a device configured as something nobody asked for.
*/
func TestParamsJSONCarriesRangesAsText(t *testing.T) {
	original := Params{
		Jc: 4, Jmin: 40, Jmax: 70,
		H1: HeaderRange(10, 19), H2: HeaderRange(20, 20),
		RekeyAfterTime: TimerRange(100, 140),
		RandomTrailers: true,
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Written the way a person reads it, so the form field shows what they typed.
	for _, want := range []string{`"h1":"10-19"`, `"h2":"20"`, `"rekeyAfterTime":"100-140"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("%s missing from %s", want, encoded)
		}
	}

	var back Params
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != original {
		t.Fatalf("round trip changed the parameters:\n got %+v\nwant %+v", back, original)
	}
}

// Settings written before ranges were text carry plain numbers, and a bare
// number is the range [n, n] -- not the packed value, which would be [n, 0].
func TestParamsJSONStillReadsPlainNumbers(t *testing.T) {
	var params Params
	if err := json.Unmarshal([]byte(`{"jc":4,"h1":10,"rekeyAfterTime":120}`), &params); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if params.H1 != HeaderRange(10, 10) {
		t.Errorf("h1 = %d, want the range [10,10] = %d", params.H1, HeaderRange(10, 10))
	}
	if params.RekeyAfterTime != TimerRange(120, 120) {
		t.Errorf("rekeyAfterTime = %d, want the range [120,120]", params.RekeyAfterTime)
	}
}

// A range the operator inverted must be refused at the API rather than stored.
func TestParamsJSONRefusesAnInvertedRange(t *testing.T) {
	var params Params
	err := json.Unmarshal([]byte(`{"h1":"19-10"}`), &params)
	if err == nil {
		t.Fatal("an inverted header range was accepted")
	}
	if !strings.Contains(err.Error(), "h1") {
		t.Errorf("the error does not name the field: %v", err)
	}
}
