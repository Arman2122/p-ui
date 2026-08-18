package awg

/*
The module's netlink vocabulary, transcribed from
third_party/amneziawg-kernel/src/uapi/wireguard.h.

Transcribed rather than derived, because Go cannot read a C enum -- and pinned
by uapi_test.go, which parses that header and fails if upstream ever inserts an
attribute. An attribute inserted mid-enum shifts every number after it, so a
re-vendor would otherwise turn "set the junk count" into "set the padding" with
nothing to notice: the kernel would accept the message and the device would be
configured wrongly.
*/
const (
	// FamilyName is why wgctrl cannot drive this module: that client binds
	// "wireguard" at compile time, and the two families are separate registries.
	FamilyName    = "amneziawg"
	FamilyVersion = 3

	// LinkType is the rtnetlink kind for `ip link add … type amneziawg`.
	LinkType = "amneziawg"
)

// Commands, from enum wg_cmd.
const (
	cmdGetDevice uint8 = iota
	cmdSetDevice
	cmdUnknownPeer
)

// Device attributes, from enum wgdevice_attribute. The order IS the protocol.
const (
	devUnspec uint16 = iota
	devIfIndex
	devIfName
	devPrivateKey
	devPublicKey
	devFlags
	devListenPort
	devFwmark
	devPeers
	devJc
	devJmin
	devJmax
	devS1
	devS2
	devH1
	devH2
	devH3
	devH4
	devPeer
	devS3
	devS4
	devI1
	devI2
	devI3
	devI4
	devI5
	devHeaderProtectionKey
	devContentPaddingAddition
	devRekeyAfterTime
	devRekeyTimeout
	devRejectAfterTime
	devKeepaliveTimeout
	devMaxHandshakeAttempts
	devRandomTrailers
	devDisableCookies
)

// Peer attributes, from enum wgpeer_attribute. ADVANCED_SECURITY is AmneziaWG's
// own: obfuscation is negotiated per peer, not only per device.
const (
	peerUnspec uint16 = iota
	peerPublicKey
	peerPresharedKey
	peerFlags
	peerEndpoint
	peerPersistentKeepaliveInterval
	peerLastHandshakeTime
	peerRxBytes
	peerTxBytes
	peerAllowedIPs
	peerProtocolVersion
	peerAdvancedSecurity
)

// Allowed-IP attributes, from enum wgallowedip_attribute.
const (
	allowedIPUnspec uint16 = iota
	allowedIPFamily
	allowedIPAddr
	allowedIPCidrMask
	allowedIPFlags
)

// The flags that make a message authoritative rather than additive.
const (
	deviceReplacePeers uint32 = 1 << 0
	peerRemoveMe       uint32 = 1 << 0
	peerReplaceAllowed uint32 = 1 << 1
	peerUpdateOnly     uint32 = 1 << 2
	peerHasAdvancedSec uint32 = 1 << 3
)
