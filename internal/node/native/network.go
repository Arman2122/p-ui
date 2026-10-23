package native

import (
	"bytes"
	"encoding/json"
	"io"
	"net/netip"

	bolt "go.etcd.io/bbolt"
)

type NetworkBinding struct {
	PeerIPv4    netip.Addr `json:"peerIPv4"`
	GatewayIPv4 netip.Addr `json:"gatewayIPv4"`
}

func DefaultNetworkBinding() NetworkBinding {
	return NetworkBinding{PeerIPv4: netip.MustParseAddr("169.254.234.2"), GatewayIPv4: netip.MustParseAddr("169.254.234.3")}
}

func (n NetworkBinding) Validate() error {
	for _, address := range []netip.Addr{n.PeerIPv4, n.GatewayIPv4} {
		if !address.Is4() || address.Zone() != "" || !address.IsPrivate() && !address.IsLinkLocalUnicast() || netip.MustParsePrefix("169.254.0.0/24").Contains(address) || netip.MustParsePrefix("169.254.255.0/24").Contains(address) {
			return ErrAuthority
		}
	}
	if n.PeerIPv4 == n.GatewayIPv4 {
		return ErrAuthority
	}
	return nil
}

type NetworkState struct {
	Current *NetworkBinding `json:"current,omitempty"`
	Pending *NetworkBinding `json:"pending,omitempty"`
}

func (s NetworkState) Target() (NetworkBinding, bool, error) {
	for _, binding := range []*NetworkBinding{s.Current, s.Pending} {
		if binding != nil && binding.Validate() != nil {
			return NetworkBinding{}, false, ErrAuthority
		}
	}
	if s.Pending != nil {
		if s.Current != nil && *s.Current == *s.Pending {
			return NetworkBinding{}, false, ErrAuthority
		}
		return *s.Pending, true, nil
	}
	if s.Current != nil {
		return *s.Current, true, nil
	}
	return NetworkBinding{}, false, nil
}

func readNetwork(tx *bolt.Tx) (NetworkState, error) {
	var state NetworkState
	data := tx.Bucket([]byte("identity")).Get([]byte("network-binding"))
	if data == nil {
		return state, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 4096 || decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF {
		return state, ErrAuthority
	}
	_, bound, err := state.Target()
	if err != nil || !bound {
		return state, ErrAuthority
	}
	return state, nil
}

func (j *Journal) Network() (NetworkState, error) {
	var state NetworkState
	err := j.db.View(func(tx *bolt.Tx) error {
		var err error
		state, err = readNetwork(tx)
		return err
	})
	return state, err
}

func networkIdle(tx *bolt.Tx) error {
	return tx.Bucket(grantsBucket).ForEach(func(key, _ []byte) error {
		record, err := readGrant(tx, string(key))
		if err != nil {
			return err
		}
		if record.Lease != nil || record.SelectorsPending || record.PendingAuthority != nil {
			return ErrBusy
		}
		return nil
	})
}

func (j *Journal) StageNetwork(target NetworkBinding) (NetworkState, error) {
	var state NetworkState
	if err := target.Validate(); err != nil {
		return state, err
	}
	err := j.db.Update(func(tx *bolt.Tx) error {
		var err error
		state, err = readNetwork(tx)
		if err != nil {
			return err
		}
		if state.Pending != nil {
			if *state.Pending != target {
				return ErrBusy
			}
			return networkIdle(tx)
		}
		if state.Current != nil && *state.Current == target {
			return nil
		}
		if err := networkIdle(tx); err != nil {
			return err
		}
		state.Pending = &target
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		return tx.Bucket([]byte("identity")).Put([]byte("network-binding"), data)
	})
	return state, err
}

func (j *Journal) CommitNetwork(target NetworkBinding) error {
	return j.db.Update(func(tx *bolt.Tx) error {
		state, err := readNetwork(tx)
		if err != nil {
			return err
		}
		if state.Current != nil && *state.Current == target && state.Pending == nil {
			return nil
		}
		if state.Pending == nil || *state.Pending != target {
			return ErrAuthority
		}
		if err := networkIdle(tx); err != nil {
			return err
		}
		data, err := json.Marshal(NetworkState{Current: &target})
		if err != nil {
			return err
		}
		return tx.Bucket([]byte("identity")).Put([]byte("network-binding"), data)
	})
}
