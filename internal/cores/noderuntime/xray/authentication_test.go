package xrayruntime

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/proxy"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func hotEcho(t *testing.T, network string) string {
	t.Helper()
	if network == "udp" {
		server, err := net.ListenPacket(network, "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			data := make([]byte, 65535)
			for {
				n, peer, err := server.ReadFrom(data)
				if err != nil {
					return
				}
				_, _ = server.WriteTo(data[:n], peer)
			}
		}()
		t.Cleanup(func() { _ = server.Close(); <-done })
		return server.LocalAddr().String()
	}
	server, err := net.Listen(network, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var tasks sync.WaitGroup
	tasks.Go(func() {
		for {
			conn, err := server.Accept()
			if err != nil {
				return
			}
			tasks.Go(func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
				_, _ = io.Copy(conn, conn)
			})
		}
	})
	t.Cleanup(func() { _ = server.Close(); tasks.Wait() })
	return server.Addr().String()
}

func hotAccount(t *testing.T) *limits.Account {
	t.Helper()
	account, err := limits.New(limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 1 << 20, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func hotMeasured(t *testing.T, account *limits.Account, expected uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		got := account.Snapshot()
		if got.MeasuredUpload == expected && got.MeasuredDownload == expected {
			return
		}
		if got.MeasuredUpload > expected || got.MeasuredDownload > expected || time.Now().After(deadline) {
			t.Fatalf("unchanged accounting changed: %+v expected=%d", got, expected)
		}
		time.Sleep(time.Millisecond)
	}
}

func hotDescriptor(client string) admission.Descriptor {
	return admission.Descriptor{ClientID: client, InboundTag: "hot", CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString()}
}

func hotExchange(conn net.Conn, data []byte) error {
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write(data); err != nil {
		return err
	}
	got := make([]byte, len(data))
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	if !bytes.Equal(data, got) {
		return fmt.Errorf("echo payload differs")
	}
	return nil
}

func TestXrayHotMixedPreservesUnchangedTrafficAndRetiresPausedAuthentication(t *testing.T) {
	tcpTarget, udpTarget := hotEcho(t, "tcp"), hotEcho(t, "udp")
	port := availablePort(t)
	endpoint := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	a, b, replacement := hotDescriptor(uuid.NewString()), hotDescriptor(uuid.NewString()), hotDescriptor(uuid.NewString())
	accounts := map[string]*limits.Account{a.ClientID: hotAccount(t), b.ClientID: hotAccount(t), replacement.ClientID: hotAccount(t)}
	lookup := func(id string) (*limits.Account, bool) { account, ok := accounts[id]; return account, ok }
	broker := admission.New("xray", func() (time.Time, error) { return time.Now(), nil })
	var runtime Runtime
	t.Cleanup(func() { _ = runtime.Close() })
	config := func(first *admission.Descriptor, password string) []byte {
		users := []map[string]string{{"user": "alice", "pass": "stable-b"}}
		identities := []ClientIdentity{{InboundTag: "hot", Username: "alice", ClientID: b.ClientID}}
		if first != nil {
			users = append(users, map[string]string{"user": "Alice", "pass": password})
			identities = append(identities, ClientIdentity{InboundTag: "hot", Username: "Alice", ClientID: first.ClientID})
		}
		data, err := json.Marshal(map[string]any{
			"log": map[string]any{"loglevel": "none"}, "penhoon": Metadata{Version: 1, ClientIdentities: identities},
			"inbounds":  []map[string]any{{"tag": "hot", "listen": "127.0.0.1", "port": port, "protocol": "mixed", "settings": map[string]any{"auth": "password", "udp": true, "accounts": users}}},
			"outbounds": []map[string]any{{"protocol": "freedom", "settings": map[string]any{"finalRules": []map[string]any{{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	revision := uint64(0)
	apply := func(first *admission.Descriptor, password string) {
		t.Helper()
		descriptors := []admission.Descriptor{b}
		if first != nil {
			descriptors = append(descriptors, *first)
		}
		binding, err := broker.Prepare(descriptors)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.InvalidateAuthentication(t.Context(), []admission.Subject{{ClientID: a.ClientID, InboundTag: "hot"}, {ClientID: replacement.ClientID, InboundTag: "hot"}}); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Apply(admission.WithBinding(t.Context(), binding), config(first, password), lookup); err != nil {
			t.Fatal(err)
		}
		revision++
		if err := broker.Commit(revision, binding); err != nil {
			t.Fatal(err)
		}
	}
	dial := func(username, password string) net.Conn {
		t.Helper()
		dialer, err := proxy.SOCKS5("tcp", endpoint, &proxy.Auth{User: username, Password: password}, &net.Dialer{Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := dialer.Dial("tcp", tcpTarget)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	apply(&a, "old-a")
	instance := runtime.instance
	bTCP, aTCP := dial("alice", "stable-b"), dial("Alice", "old-a")
	if err := hotExchange(aTCP, []byte("old-a-live")); err != nil {
		t.Fatal(err)
	}
	bHTTP, err := net.DialTimeout("tcp", endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bHTTP.Close() })
	_, _ = fmt.Fprintf(bHTTP, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", tcpTarget, tcpTarget, base64.StdEncoding.EncodeToString([]byte("alice:stable-b")))
	response, err := http.ReadResponse(bufio.NewReader(bHTTP), &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP connect=%v error=%v", response, err)
	}
	bUDP, exchangeUDP := hotMixedUDP(t, endpoint, udpTarget, "alice", "stable-b")
	defer bUDP.Close()
	var expected uint64
	checkB := func() {
		t.Helper()
		if runtime.instance != instance {
			t.Fatal("unchanged Xray listener restarted during hot authentication")
		}
		for _, conn := range []net.Conn{bTCP, bHTTP} {
			if err := hotExchange(conn, []byte("unchanged-b")); err != nil {
				t.Fatalf("unchanged B TCP session interrupted: %v", err)
			}
		}
		if err := exchangeUDP([]byte("unchanged-b")); err != nil {
			t.Fatalf("unchanged B UDP association interrupted: %v", err)
		}
		expected += 33
		hotMeasured(t, accounts[b.ClientID], expected)
	}
	checkB()
	paused := hotMixedAuthentication(t, endpoint, "Alice", "old-a")
	defer paused.Close()
	a.CredentialGeneration++
	apply(&a, "new-a")
	checkB()
	if err := hotExchange(aTCP, []byte("retired-a")); err == nil {
		t.Fatal("retired funded A connection forwarded")
	}
	if err := hotExchange(dial("Alice", "new-a"), []byte("new-a")); err != nil {
		t.Fatal(err)
	}
	apply(nil, "")
	checkB()
	apply(&replacement, "old-a")
	checkB()
	before := accounts[replacement.ClientID].Snapshot()
	request := hotSOCKSRequest(t, 1, tcpTarget)
	_, _ = paused.Write(append(request, []byte("must-not-relabel")...))
	_ = paused.SetReadDeadline(time.Now().Add(time.Second))
	var reply [10]byte
	_, _ = io.ReadFull(paused, reply[:])
	var one [1]byte
	if n, err := paused.Read(one[:]); n != 0 || err == nil {
		t.Fatal("paused old authentication used reused funded alias")
	}
	if got := accounts[replacement.ClientID].Snapshot(); got.MeasuredUpload != before.MeasuredUpload || got.MeasuredDownload != before.MeasuredDownload {
		t.Fatal("paused authentication charged replacement account")
	}
	if err := hotExchange(dial("Alice", "old-a"), []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	checkB()
	replacement.PlacementRevision++
	failed, err := broker.Prepare([]admission.Descriptor{replacement, b})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Apply(admission.WithBinding(t.Context(), failed), []byte(`{"inbounds":`), lookup); err == nil {
		t.Fatal("invalid hot configuration succeeded")
	}
	checkB()
	apply(&replacement, "old-a")
	checkB()
}

func hotMixedAuthentication(t *testing.T, endpoint, username, password string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte{5, 1, 2})
	var response [2]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil || response != [2]byte{5, 2} {
		t.Fatalf("SOCKS method=%v error=%v", response, err)
	}
	auth := append([]byte{1, byte(len(username))}, username...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, password...)
	_, _ = conn.Write(auth)
	if _, err := io.ReadFull(conn, response[:]); err != nil || response != [2]byte{1, 0} {
		t.Fatalf("SOCKS authentication=%v error=%v", response, err)
	}
	return conn
}

func hotSOCKSRequest(t *testing.T, command byte, target string) []byte {
	t.Helper()
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	request := append([]byte{5, command, 0, 1}, net.ParseIP(host).To4()...)
	return binary.BigEndian.AppendUint16(request, uint16(number))
}

func hotMixedUDP(t *testing.T, endpoint, target, username, password string) (net.Conn, func([]byte) error) {
	t.Helper()
	control := hotMixedAuthentication(t, endpoint, username, password)
	_, _ = control.Write(hotSOCKSRequest(t, 3, "0.0.0.0:0"))
	var response [10]byte
	if _, err := io.ReadFull(control, response[:]); err != nil || response[1] != 0 || response[3] != 1 {
		t.Fatalf("SOCKS UDP=%v error=%v", response, err)
	}
	packet, err := net.Dial("udp", net.JoinHostPort(net.IP(response[4:8]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(response[8:])))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = packet.Close() })
	address := hotSOCKSRequest(t, 0, target)[3:]
	return control, func(payload []byte) error {
		_ = packet.SetDeadline(time.Now().Add(time.Second))
		message := append(append([]byte{0, 0, 0}, address...), payload...)
		if _, err := packet.Write(message); err != nil {
			return err
		}
		response := make([]byte, len(message)+16)
		n, err := packet.Read(response)
		if err != nil {
			return err
		}
		if !bytes.Equal(response[:n], message) {
			return fmt.Errorf("UDP echo differs")
		}
		return nil
	}
}

func TestXrayHotHTTPEmptyUsersStillRequireAuthentication(t *testing.T) {
	port := availablePort(t)
	client := uuid.NewString()
	account := hotAccount(t)
	var runtime Runtime
	t.Cleanup(func() { _ = runtime.Close() })
	configure := func(include bool) {
		t.Helper()
		var users []map[string]string
		var identities []ClientIdentity
		if include {
			users = append(users, map[string]string{"user": "only-user", "pass": "only-password"})
			identities = append(identities, ClientIdentity{InboundTag: "hot", Username: "only-user", ClientID: client})
		}
		data, err := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "none"}, "penhoon": Metadata{Version: 1, ClientIdentities: identities}, "inbounds": []map[string]any{{"tag": "hot", "protocol": "http", "listen": "127.0.0.1", "port": port, "settings": map[string]any{"accounts": users}}}, "outbounds": []map[string]any{{"protocol": "blackhole"}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Apply(t.Context(), data, func(id string) (*limits.Account, bool) { return account, id == client }); err != nil {
			t.Fatal(err)
		}
	}
	configure(true)
	instance := runtime.instance
	configure(false)
	if runtime.instance != instance {
		t.Fatal("removing last HTTP user restarted runtime")
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = io.WriteString(conn, "CONNECT 127.0.0.1:1 HTTP/1.1\r\nHost: 127.0.0.1:1\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatal("empty governed HTTP credential store allowed anonymous authentication")
	}
}
