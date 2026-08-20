package awg

import (
	"encoding/json"
	"fmt"
)

/*
Params crosses the API with its ranges as TEXT.

A header and a timer are ranges the kernel wants packed -- (hi<<32|lo) and
(hi<<16|lo) -- and nobody types that. Stored and rendered as "10-19" they are
what an operator would write in a .conf, what awg-tools prints, and what can be
shown back in a form field unchanged.

Numbers are still accepted on the way in, because settings written before this
carry them, and a bare number means the range [n, n] exactly as awg-tools reads
one. That is the whole hazard in one sentence: taken as a raw packed value
instead, 10 is the range [10, 0] -- an upper bound below its lower one, which
nothing intersects and which the kernel's overlap check therefore never fires on.
*/

type paramsWire struct {
	Jc   uint16 `json:"jc,omitempty"`
	Jmin uint16 `json:"jmin,omitempty"`
	Jmax uint16 `json:"jmax,omitempty"`
	S1   uint16 `json:"s1,omitempty"`
	S2   uint16 `json:"s2,omitempty"`
	S3   uint16 `json:"s3,omitempty"`
	S4   uint16 `json:"s4,omitempty"`

	H1 json.RawMessage `json:"h1,omitempty"`
	H2 json.RawMessage `json:"h2,omitempty"`
	H3 json.RawMessage `json:"h3,omitempty"`
	H4 json.RawMessage `json:"h4,omitempty"`

	I1 string `json:"i1,omitempty"`
	I2 string `json:"i2,omitempty"`
	I3 string `json:"i3,omitempty"`
	I4 string `json:"i4,omitempty"`
	I5 string `json:"i5,omitempty"`

	HeaderProtectionKey string `json:"headerProtectionKey,omitempty"`
	RandomTrailers      bool   `json:"randomTrailers,omitempty"`
	DisableCookies      bool   `json:"disableCookies,omitempty"`

	ContentPaddingAddition json.RawMessage `json:"contentPaddingAddition,omitempty"`
	RekeyAfterTime         json.RawMessage `json:"rekeyAfterTime,omitempty"`
	RekeyTimeout           json.RawMessage `json:"rekeyTimeout,omitempty"`
	RejectAfterTime        json.RawMessage `json:"rejectAfterTime,omitempty"`
	KeepaliveTimeout       json.RawMessage `json:"keepaliveTimeout,omitempty"`
	MaxHandshakeAttempts   json.RawMessage `json:"maxHandshakeAttempts,omitempty"`
}

func (p *Params) UnmarshalJSON(data []byte) error {
	var wire paramsWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*p = Params{
		Jc: wire.Jc, Jmin: wire.Jmin, Jmax: wire.Jmax,
		S1: wire.S1, S2: wire.S2, S3: wire.S3, S4: wire.S4,
		I1: wire.I1, I2: wire.I2, I3: wire.I3, I4: wire.I4, I5: wire.I5,
		HeaderProtectionKey: wire.HeaderProtectionKey,
		RandomTrailers:      wire.RandomTrailers,
		DisableCookies:      wire.DisableCookies,
	}

	for _, field := range []struct {
		name string
		raw  json.RawMessage
		into *uint64
	}{
		{"h1", wire.H1, &p.H1},
		{"h2", wire.H2, &p.H2},
		{"h3", wire.H3, &p.H3},
		{"h4", wire.H4, &p.H4},
	} {
		value, err := decodeRange(field.raw, ParseHeaderRange)
		if err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
		*field.into = value
	}

	for _, field := range []struct {
		name string
		raw  json.RawMessage
		into *uint32
	}{
		{"contentPaddingAddition", wire.ContentPaddingAddition, &p.ContentPaddingAddition},
		{"rekeyAfterTime", wire.RekeyAfterTime, &p.RekeyAfterTime},
		{"rekeyTimeout", wire.RekeyTimeout, &p.RekeyTimeout},
		{"rejectAfterTime", wire.RejectAfterTime, &p.RejectAfterTime},
		{"keepaliveTimeout", wire.KeepaliveTimeout, &p.KeepaliveTimeout},
		{"maxHandshakeAttempts", wire.MaxHandshakeAttempts, &p.MaxHandshakeAttempts},
	} {
		value, err := decodeRange(field.raw, ParseTimerRange)
		if err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
		*field.into = value
	}
	return nil
}

func (p Params) MarshalJSON() ([]byte, error) {
	wire := paramsWire{
		Jc: p.Jc, Jmin: p.Jmin, Jmax: p.Jmax,
		S1: p.S1, S2: p.S2, S3: p.S3, S4: p.S4,
		I1: p.I1, I2: p.I2, I3: p.I3, I4: p.I4, I5: p.I5,
		HeaderProtectionKey: p.HeaderProtectionKey,
		RandomTrailers:      p.RandomTrailers,
		DisableCookies:      p.DisableCookies,
	}
	wire.H1 = encodeRange(FormatHeaderRange(p.H1))
	wire.H2 = encodeRange(FormatHeaderRange(p.H2))
	wire.H3 = encodeRange(FormatHeaderRange(p.H3))
	wire.H4 = encodeRange(FormatHeaderRange(p.H4))
	wire.ContentPaddingAddition = encodeRange(FormatTimerRange(p.ContentPaddingAddition))
	wire.RekeyAfterTime = encodeRange(FormatTimerRange(p.RekeyAfterTime))
	wire.RekeyTimeout = encodeRange(FormatTimerRange(p.RekeyTimeout))
	wire.RejectAfterTime = encodeRange(FormatTimerRange(p.RejectAfterTime))
	wire.KeepaliveTimeout = encodeRange(FormatTimerRange(p.KeepaliveTimeout))
	wire.MaxHandshakeAttempts = encodeRange(FormatTimerRange(p.MaxHandshakeAttempts))
	return json.Marshal(wire)
}

// decodeRange takes either the text form or a number written by an older
// settings blob, and treats a bare number as the range [n, n].
func decodeRange[T uint32 | uint64](raw json.RawMessage, parse func(string) (T, error)) (T, error) {
	var zero T
	if len(raw) == 0 || string(raw) == "null" {
		return zero, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return parse(text)
	}
	var number uint64
	if err := json.Unmarshal(raw, &number); err != nil {
		return zero, fmt.Errorf("%w: %s", ErrRangeText, raw)
	}
	return parse(fmt.Sprintf("%d", number))
}

func encodeRange(text string) json.RawMessage {
	if text == "" {
		return nil
	}
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return encoded
}
