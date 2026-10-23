//go:build linux

package native

import (
	"encoding/json"
	"net/netip"
)

func mapElement(raw json.RawMessage) (Selector, string, error) {
	var pair []json.RawMessage
	if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
		return Selector{}, "", ErrAuthority
	}
	var key struct {
		Concat []string
		Elem   *struct{ Val struct{ Concat []string } }
	}
	var verdict struct{ Goto *struct{ Target string } }
	if json.Unmarshal(pair[0], &key) != nil || json.Unmarshal(pair[1], &verdict) != nil || verdict.Goto == nil {
		return Selector{}, "", ErrAuthority
	}
	if key.Elem != nil {
		key.Concat = key.Elem.Val.Concat
	}
	if len(key.Concat) != 2 {
		return Selector{}, "", ErrAuthority
	}
	address, err := netip.ParseAddr(key.Concat[1])
	if err != nil {
		return Selector{}, "", ErrAuthority
	}
	selector := Selector{Device: key.Concat[0], Address: address}
	if validateSelectors([]Selector{selector}) != nil {
		return Selector{}, "", ErrAuthority
	}
	return selector, verdict.Goto.Target, nil
}
