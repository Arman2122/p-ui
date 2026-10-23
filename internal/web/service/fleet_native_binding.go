package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/native"
)

var ErrFleetNativeBinding = errors.New("native address allocation requires reconciliation")

type FleetNativeBindingPin struct {
	ID                string       `json:"id"`
	PlacementID       string       `json:"placementId"`
	ClientID          string       `json:"clientId"`
	InboundInstanceID string       `json:"inboundInstanceId"`
	TargetID          uint32       `json:"targetId"`
	PoolSHA256        string       `json:"poolSha256"`
	Prefix            netip.Prefix `json:"prefix"`
	MaxSessions       uint16       `json:"maxSessions"`
}

func bindingAddresses(prefix netip.Prefix) ([]netip.Addr, error) {
	if !prefix.IsValid() || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() || prefix != prefix.Masked() || prefix.Bits() < 24 {
		return nil, ErrFleetNativeBinding
	}
	count := 1 << uint(32-prefix.Bits())
	result := make([]netip.Addr, 0, count)
	for address := prefix.Addr(); prefix.Contains(address); address = address.Next() {
		result = append(result, address)
	}
	return result, nil
}

func prepareFleetNativeBindings(tx *gorm.DB, state fleetNetworkState, input *nodeconfig.Input, manifest *FleetNetworkManifest) ([]FleetNetworkDiagnostic, error) {
	saved, _, err := loadFleetNativeBindings(tx, state.Node)
	if err != nil {
		return nil, err
	}
	bySubject := make(map[string]model.FleetNativeBinding, len(saved))
	occupied := make(map[netip.Addr]string)
	network := native.DefaultNetworkBinding()
	if input.NativeNetwork != nil {
		network = *input.NativeNetwork
	}
	if network.Validate() != nil {
		return nil, ErrFleetNativeBinding
	}
	occupied[network.PeerIPv4], occupied[network.GatewayIPv4] = "shaping-peer", "shaping-gateway"
	for _, binding := range saved {
		prefix, err := netip.ParsePrefix(binding.Prefix)
		if err != nil {
			return nil, ErrFleetNativeBinding
		}
		addresses, err := bindingAddresses(prefix)
		if err != nil {
			return nil, err
		}
		key := binding.InboundInstanceID + "/" + binding.ClientID
		if bySubject[key].ID != "" {
			return nil, ErrFleetNativeBinding
		}
		bySubject[key] = binding
		for _, address := range addresses {
			if occupied[address] != "" {
				return nil, ErrFleetNativeBinding
			}
			occupied[address] = binding.ID
		}
	}
	identities := make(map[string]FleetNetworkAuthorityIdentity)
	for _, identity := range manifest.Identities {
		identities[identity.RuntimeID+"/"+identity.ClientID] = identity
	}
	requests := make(map[int]*nodeconfig.NativeAddressRequest)
	for i, inbound := range input.Inbounds {
		request, err := nodeconfig.NativeAddressRequestFor(inbound)
		if err != nil {
			return []FleetNetworkDiagnostic{{Code: "NATIVE_ADDRESS_POOL_INVALID", InboundID: inbound.Instance.ID}}, nil
		}
		requests[i] = request
		if request != nil {
			for _, address := range request.Reserved {
				if occupied[address] != "" {
					return []FleetNetworkDiagnostic{{Code: "NATIVE_ADDRESS_POOL_CONFLICT", InboundID: inbound.Instance.ID}}, nil
				}
				occupied[address] = "server"
			}
			continue
		}
		addresses, err := nodeconfig.NativeExplicitAddresses(inbound)
		if err != nil {
			return []FleetNetworkDiagnostic{{Code: "NATIVE_EXPLICIT_ADDRESS_INVALID", InboundID: inbound.Instance.ID}}, nil
		}
		for _, address := range addresses {
			if occupied[address] != "" {
				return []FleetNetworkDiagnostic{{Code: "NATIVE_ADDRESS_POOL_CONFLICT", InboundID: inbound.Instance.ID}}, nil
			}
			occupied[address] = "explicit"
		}
	}
	var issues []FleetNetworkDiagnostic
	for i := range input.Inbounds {
		request := requests[i]
		if request == nil {
			continue
		}
		inbound := &input.Inbounds[i]
		encoded, err := json.Marshal(request)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(encoded)
		slices.SortFunc(inbound.Clients, func(a, b nodeconfig.Client) int { return strings.Compare(a.ClientID, b.ClientID) })
		for j := range inbound.Clients {
			client := &inbound.Clients[j]
			identity := identities["native/"+client.ClientID]
			instance := uuid.NewSHA1(uuid.MustParse(state.Node.InstallationID), []byte("native/inbound/"+strconv.Itoa(inbound.Instance.ID))).String()
			if identity.InboundTags[instance] != inbound.TargetTag || !fleetID(identity.PlacementID) {
				return nil, ErrFleetNativeBinding
			}
			pin := FleetNativeBindingPin{PlacementID: identity.PlacementID, ClientID: client.ClientID, InboundInstanceID: instance, TargetID: inbound.TargetID, PoolSHA256: hex.EncodeToString(digest[:]), MaxSessions: request.MaxSessions}
			pin.ID = uuid.NewSHA1(uuid.MustParse(state.Node.InstallationID), []byte(instance+"/"+client.ClientID+"/"+pin.PoolSHA256)).String()
			if prior := bySubject[instance+"/"+client.ClientID]; prior.ID != "" {
				if prior.ID != pin.ID || prior.PlacementID != pin.PlacementID || prior.TargetID != pin.TargetID || prior.MaxSessions != pin.MaxSessions || !bytes.Equal(prior.PoolDigest, digest[:]) {
					issues = append(issues, FleetNetworkDiagnostic{Code: "NATIVE_BINDING_DRAIN_REQUIRED", PlacementID: pin.PlacementID, ClientID: pin.ClientID, InboundID: inbound.Instance.ID})
					continue
				}
				pin.Prefix, err = netip.ParsePrefix(prior.Prefix)
				if err != nil || !request.Pool.Contains(pin.Prefix.Addr()) || pin.Prefix.Bits() != request.PrefixBits {
					return nil, ErrFleetNativeBinding
				}
			} else {
				pin.Prefix, err = chooseNativePrefix(*request, pin.ID, occupied)
				if err != nil {
					issues = append(issues, FleetNetworkDiagnostic{Code: "NATIVE_ADDRESS_POOL_EXHAUSTED", PlacementID: pin.PlacementID, ClientID: pin.ClientID, InboundID: inbound.Instance.ID})
					continue
				}
				addresses, _ := bindingAddresses(pin.Prefix)
				for _, address := range addresses {
					occupied[address] = pin.ID
				}
			}
			if pin.Prefix.Bits() == 32 {
				client.Native.Addresses = []netip.Addr{pin.Prefix.Addr()}
			} else {
				client.Native.Subnet, client.Native.MaxSessions = pin.Prefix, pin.MaxSessions
			}
			manifest.NativeBindings = append(manifest.NativeBindings, pin)
		}
	}
	slices.SortFunc(manifest.NativeBindings, func(a, b FleetNativeBindingPin) int { return strings.Compare(a.ID, b.ID) })
	return issues, nil
}

func chooseNativePrefix(request nodeconfig.NativeAddressRequest, id string, occupied map[netip.Addr]string) (netip.Prefix, error) {
	if !request.Pool.IsValid() || !request.Pool.Addr().Is4() || request.Pool.Bits() < 16 || request.Pool.Bits() > 29 || request.PrefixBits < request.Pool.Bits() || request.PrefixBits > 32 {
		return netip.Prefix{}, ErrFleetNativeBinding
	}
	base := request.Pool.Addr().As4()
	start := binary.BigEndian.Uint32(base[:])
	count := uint32(1) << uint(request.PrefixBits-request.Pool.Bits())
	step := uint32(1) << uint(32-request.PrefixBits)
	hash := sha256.Sum256([]byte(id))
	first := binary.BigEndian.Uint32(hash[:4]) % count
	for n := uint32(0); n < count; n++ {
		value := start + ((first+n)%count)*step
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], value)
		prefix := netip.PrefixFrom(netip.AddrFrom4(raw), request.PrefixBits)
		addresses, err := bindingAddresses(prefix)
		if err != nil {
			return netip.Prefix{}, err
		}
		free := true
		for _, address := range addresses {
			if occupied[address] != "" || request.PrefixBits == 32 && !request.Pool.Contains(address.Next()) {
				free = false
				break
			}
		}
		if free {
			return prefix, nil
		}
	}
	return netip.Prefix{}, ErrFleetNativeBinding
}

func persistFleetNativeBindings(tx *gorm.DB, node model.FleetNode, revision uint64, pins []FleetNativeBindingPin) error {
	if revision == 0 || revision > math.MaxInt64 || len(pins) > 60000 || !fleetID(node.ID) || !fleetID(node.InstallationID) {
		return ErrFleetNativeBinding
	}
	rows := make([]model.FleetNativeBinding, 0, len(pins))
	ids := make([]string, 0, len(pins))
	seen := make(map[string]bool, len(pins))
	for _, pin := range pins {
		digest, err := hex.DecodeString(pin.PoolSHA256)
		if err != nil || len(digest) != sha256.Size || !fleetID(pin.ID) || !fleetID(pin.PlacementID) || !fleetID(pin.ClientID) || !fleetID(pin.InboundInstanceID) || pin.TargetID == 0 || seen[pin.ID] {
			return ErrFleetNativeBinding
		}
		if _, err := bindingAddresses(pin.Prefix); err != nil {
			return err
		}
		seen[pin.ID] = true
		row := model.FleetNativeBinding{
			ID: pin.ID, NodeID: node.ID, InstallationID: node.InstallationID, PlacementID: pin.PlacementID, ClientID: pin.ClientID,
			InboundInstanceID: pin.InboundInstanceID, TargetID: pin.TargetID, PoolDigest: digest, Prefix: pin.Prefix.String(), MaxSessions: pin.MaxSessions, PublishedRevision: int64(revision),
		}
		rows = append(rows, row)
		ids = append(ids, row.ID)
	}
	for offset := 0; offset < len(rows); offset += 512 {
		chunk := rows[offset:min(offset+512, len(rows))]
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error; err != nil {
			return err
		}
		var saved []model.FleetNativeBinding
		if err := tx.Where("id IN ?", ids[offset:min(offset+512, len(ids))]).Find(&saved).Error; err != nil {
			return err
		}
		if len(saved) != len(chunk) {
			return ErrFleetNativeBinding
		}
		byID := make(map[string]model.FleetNativeBinding, len(saved))
		for _, row := range saved {
			byID[row.ID] = row
		}
		for _, row := range chunk {
			existing := byID[row.ID]
			if existing.NodeID != row.NodeID || existing.InstallationID != row.InstallationID || existing.PlacementID != row.PlacementID || existing.ClientID != row.ClientID || existing.Prefix != row.Prefix || existing.InboundInstanceID != row.InboundInstanceID || existing.TargetID != row.TargetID || !bytes.Equal(existing.PoolDigest, row.PoolDigest) || existing.MaxSessions != row.MaxSessions {
				return ErrFleetNativeBinding
			}
		}
	}
	return nil
}
