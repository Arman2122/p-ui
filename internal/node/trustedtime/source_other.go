//go:build !linux

package trustedtime

func SystemSource() (Source, error) { return nil, ErrUntrusted }
