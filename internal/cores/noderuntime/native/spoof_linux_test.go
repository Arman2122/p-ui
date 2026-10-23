//go:build linux

package nativeruntime

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestNativeSpoofPacketHelper(t *testing.T) {
	target := os.Getenv("PENHOON_NATIVE_SPOOF_TARGET")
	if target == "" {
		t.Skip("native packet subprocess")
	}
	source := os.Getenv("PENHOON_NATIVE_SPOOF_SOURCE")
	if source == "" {
		source = "0.0.0.0"
	}
	conn, err := net.ListenPacket("udp4", net.JoinHostPort(source, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	address, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteTo([]byte("native-source-proof"), address); err != nil {
		t.Fatal(err)
	}
}

func assertNativeSpoofBlocked(t *testing.T, namespace, server, forged string) {
	t.Helper()
	listener, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	target := net.JoinHostPort(server, strconv.Itoa(listener.LocalAddr().(*net.UDPAddr).Port))
	send := func(source string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "ip", "netns", "exec", namespace, os.Args[0], "-test.run=^TestNativeSpoofPacketHelper$")
		command.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "PENHOON_NATIVE_SPOOF_TARGET="+target, "PENHOON_NATIVE_SPOOF_SOURCE="+source)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("send source proof: %v %s", err, output)
		}
	}
	send("")
	buffer := make([]byte, 256)
	_ = listener.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, peer, err := listener.ReadFrom(buffer)
	if err != nil || !bytes.Equal(buffer[:n], []byte("native-source-proof")) {
		t.Fatalf("legitimate UDP source proof failed: %v", err)
	}
	if peer.(*net.UDPAddr).IP.String() == forged {
		t.Fatal("positive source unexpectedly used forged address")
	}
	send(forged)
	_ = listener.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, peer, err = listener.ReadFrom(buffer)
	if err == nil {
		t.Fatalf("spoofed source reached native input: %s", peer)
	}
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("source proof failed without timeout: %v", err)
	}
}
