//go:build !linux

package awg

// Client exists off Linux only so callers compile. Every method refuses: the
// module is a Linux kernel module, and a silent no-op here would let a
// development build report a device it never created.
type Client struct{}

func New() (*Client, error) { return nil, ErrPlatformUnsupported }

func (c *Client) Close() error { return ErrPlatformUnsupported }

func (c *Client) ConfigureDevice(string, Config) error { return ErrPlatformUnsupported }

func (c *Client) Device(string) (*Device, error) { return nil, ErrPlatformUnsupported }
