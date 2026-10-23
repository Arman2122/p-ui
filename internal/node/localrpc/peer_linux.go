package localrpc

import (
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func verifyPeer(connection net.Conn, uid uint32) error {
	socket, ok := connection.(*net.UnixConn)
	if !ok {
		return ErrPeer
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return err
	}
	var credential *unix.Ucred
	var inner error
	if err := raw.Control(func(fd uintptr) { credential, inner = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return err
	}
	if inner != nil || credential == nil || credential.Uid != uid {
		return ErrPeer
	}
	return nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSocket == 0 {
		return ErrPeer
	}
	return os.Remove(path)
}
