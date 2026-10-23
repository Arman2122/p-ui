//go:build !linux

package localrpc

import "net"

func verifyPeer(net.Conn, uint32) error { return ErrPeer }
func removeStaleSocket(string) error    { return ErrPeer }
