package nodeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"

	"github.com/Arman2122/p-ui/internal/core"
	nativeruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/native"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

var (
	ErrInvalid       = errors.New("invalid node compiler input")
	ErrUnsupported   = errors.New("node configuration feature is not qualified")
	ErrDependency    = errors.New("node configuration dependency is unresolved")
	ErrClientBinding = fmt.Errorf("%w: explicit client network binding is required", ErrDependency)
	ErrConflict      = errors.New("node configuration resources conflict")
)

type Input struct {
	NodeID        string
	Inbounds      []Inbound
	NativeNetwork *nativeruntime.NetworkBinding
	SSHHostKeys   HostKeyBinding
	Assets        []Asset
	Bridges       []Bridge
	Bases         []Base
}

type Inbound struct {
	Instance  core.Instance
	TargetID  uint32
	TargetTag string
	Clients   []Client
	Native    NativeInboundBinding
	MTProto   *MTProtoBinding
	Assets    map[string]string
	Egress    EgressBinding
}

type MTProtoBinding struct {
	TrustedProxies []netip.Prefix
	WEB            *MTProtoWEBBinding
}

type MTProtoWEBBinding struct {
	Listen     string
	Port       uint16
	PublicPort uint16
}

type Client struct {
	ClientID string
	User     core.User
	Native   NativeClientBinding
}

type NativeInboundBinding struct {
	Addresses []netip.Prefix
	Subnet    netip.Prefix
	MTU       uint16
}

type NativeClientBinding struct {
	Addresses   []netip.Addr
	Subnet      netip.Prefix
	MaxSessions uint16
}

type HostKeyBinding struct {
	GenerateLocally bool
	AssetID         string
}

type Asset struct {
	ID     string
	NodeID string
	Kind   string
	Data   []byte
}

const (
	AssetCertificate  = "tls-certificate"
	AssetPrivateKey   = "tls-private-key"
	AssetSSHHostKeys  = "ssh-host-keys"
	AssetTLSCryptV2   = "openvpn-tls-crypt-v2-server"
	AssetWireGuardKey = "wireguard-private-key"
	AssetEmbedded     = "embedded"
	AssetFile         = "file"
	AssetGenerated    = "node-local-generation"
	EgressDirect      = "direct"
	EgressXray        = "xray"
)

type EgressBinding struct {
	Mode     string
	BridgeID string
}

type Bridge struct {
	ID        string
	NodeID    string
	RuntimeID string
	Core      string
	Endpoint  netip.AddrPort
	UDP       bool
}

type Base struct {
	RuntimeID     string
	Core          string
	Configuration json.RawMessage
	Assets        map[string]string
}

type Result struct {
	Runtimes  []RuntimeDefinition
	Listeners []ListenerClaim
	Assets    []AssetClaim
	Bridges   []BridgeClaim
	Omissions []Omission
}

type RuntimeDefinition struct {
	ID            string
	Core          string
	Configuration json.RawMessage
	ClientIDs     []string
	FileAssets    []configuration.FileAsset
}

type ListenerClaim struct {
	RuntimeID string
	SourceID  int
	TargetID  uint32
	Protocol  string
	Address   netip.Addr
	Port      uint16
	DualStack bool
	Role      string
}

type AssetClaim struct {
	AssetID   string
	RuntimeID string
	SourceID  int
	Field     string
	Kind      string
	Delivery  string
	NodePath  string
	SHA256    string
}

type BridgeClaim struct {
	ID                string
	RuntimeID         string
	ConsumerRuntimeID string
	SourceID          int
	TargetID          uint32
	Endpoint          netip.AddrPort
	UDP               bool
}

type Omission struct {
	SourceID int
	ClientID string
	Reason   string
}
