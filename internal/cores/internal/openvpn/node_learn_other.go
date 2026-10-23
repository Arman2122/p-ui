//go:build !linux

package openvpn

func NodeLearn([]string) error { return ErrNodeConfiguration }
