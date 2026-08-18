//go:build linux

package awg

import (
	"fmt"

	"github.com/mdlayher/genetlink"
	"github.com/mdlayher/netlink"
)

/*
Client talks to the AmneziaWG module over its own generic netlink family.

Not wgctrl: that client resolves the family "wireguard" at compile time and has
no vocabulary for the obfuscation attributes, so it can neither reach this module
nor say anything it needs to hear.
*/
type Client struct {
	conn   *genetlink.Conn
	family genetlink.Family
}

// New resolves the family by name, which is also the honest test of whether the
// module is loaded: an absent family is an absent module, and saying so here
// beats an unexplained EINVAL when the first device is configured.
func New() (*Client, error) {
	conn, err := genetlink.Dial(nil)
	if err != nil {
		return nil, fmt.Errorf("awg: dialling generic netlink: %w", err)
	}
	family, err := conn.GetFamily(FamilyName)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %s", ErrModuleAbsent, err)
	}
	return &Client{conn: conn, family: family}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

/*
ConfigureDevice applies one configuration, splitting it across as many messages
as the peers need.

The split is upstream's own instruction and its hazard is specific: REPLACE_PEERS
must ride on the FIRST message only. Sent on every fragment, each one clears the
peers the last one just installed, and a device with more clients than fit in a
single message ends up serving only the final chunk -- silently, because every
message is accepted.
*/
func (c *Client) ConfigureDevice(name string, cfg Config) error {
	attrs, err := encodeConfig(name, cfg)
	if err != nil {
		return err
	}
	peers, err := encodePeerChunks(cfg.Peers)
	if err != nil {
		return err
	}

	// The first message carries the device itself: keys, port, parameters, and
	// the replace flag if one was asked for.
	first := attrs
	if len(peers) > 0 {
		first = append(first, netlink.Attribute{Type: devPeers | netlink.Nested, Data: peers[0]})
	}
	if err := c.execute(first); err != nil {
		return err
	}

	// From the second chunk on. A device with no peers has none of these, and
	// slicing past the end of an empty list is how that came out as a panic.
	for i := 1; i < len(peers); i++ {
		chunk := peers[i]
		// Deliberately NOT carrying the flags: a repeated REPLACE_PEERS would
		// wipe everything the previous fragments installed.
		follow := []netlink.Attribute{
			{Type: devIfName, Data: nlNulString(name)},
			{Type: devPeers | netlink.Nested, Data: chunk},
		}
		if err := c.execute(follow); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) execute(attrs []netlink.Attribute) error {
	data, err := netlink.MarshalAttributes(attrs)
	if err != nil {
		return fmt.Errorf("awg: marshalling attributes: %w", err)
	}
	_, err = c.conn.Execute(
		genetlink.Message{
			Header: genetlink.Header{Command: cmdSetDevice, Version: FamilyVersion},
			Data:   data,
		},
		c.family.ID,
		netlink.Request|netlink.Acknowledge,
	)
	if err != nil {
		return fmt.Errorf("awg: the module refused the configuration: %w", err)
	}
	return nil
}
