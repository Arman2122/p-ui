//go:build ignore

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
)

const probeRate = 128000
const probeBurst = 65536
const packetEnvelope = 2048

type reading struct {
	Bytes               uint64
	Packets             uint32
	ElapsedMilliseconds int64
	EnvelopeBytes       uint64
}

type probe struct {
	link       netlink.Link
	connection net.Conn
	class      *netlink.HtbClass
	leaf       netlink.Qdisc
}

func (p *probe) spec() *netlink.HtbClass {
	return netlink.NewHtbClass(netlink.ClassAttrs{LinkIndex: p.link.Attrs().Index, Parent: netlink.MakeHandle(1, 0), Handle: netlink.MakeHandle(1, 1)}, netlink.HtbClassAttrs{Rate: probeRate, Ceil: probeRate, Buffer: probeBurst, Cbuffer: probeBurst, Quantum: 1600})
}

func (p *probe) create(empty bool) error {
	p.class = p.spec()
	if empty {
		p.class.Buffer, p.class.Cbuffer = 0, 0
	}
	if err := netlink.ClassAdd(p.class); err != nil {
		return err
	}
	if empty {
		p.class = p.spec()
		if err := netlink.ClassChange(p.class); err != nil {
			return err
		}
	}
	p.leaf = &netlink.GenericQdisc{QdiscAttrs: netlink.QdiscAttrs{LinkIndex: p.link.Attrs().Index, Parent: netlink.MakeHandle(1, 1), Handle: netlink.MakeHandle(2, 0)}, QdiscType: "pfifo"}
	return netlink.QdiscReplace(p.leaf)
}

func (p *probe) stats() (reading, error) {
	classes, err := netlink.ClassList(p.link, 0)
	if err != nil {
		return reading{}, err
	}
	for _, class := range classes {
		if class.Attrs().Handle == netlink.MakeHandle(1, 1) && class.Attrs().Statistics != nil && class.Attrs().Statistics.Basic != nil {
			stats := class.Attrs().Statistics.Basic
			return reading{Bytes: stats.Bytes, Packets: stats.Packets}, nil
		}
	}
	return reading{}, errors.New("probe class statistics missing")
}

func (p *probe) send(count int) error {
	if err := p.connection.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	for range count {
		if _, err := p.connection.Write(make([]byte, 1024)); err != nil {
			return err
		}
	}
	return nil
}

func (p *probe) measure() (reading, error) {
	before, err := p.stats()
	if err != nil {
		return reading{}, err
	}
	started := time.Now()
	if err := p.send(80); err != nil {
		return reading{}, err
	}
	time.Sleep(50 * time.Millisecond)
	after, err := p.stats()
	if err != nil {
		return reading{}, err
	}
	elapsed := time.Since(started)
	after.Bytes -= before.Bytes
	after.Packets -= before.Packets
	after.ElapsedMilliseconds = elapsed.Milliseconds()
	after.EnvelopeBytes = packetEnvelope + uint64(elapsed.Seconds()*float64(probeRate/8))
	return after, nil
}

func run() error {
	host, err := os.Readlink("/proc/1/ns/net")
	if err != nil {
		return err
	}
	current, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return err
	}
	if host == current || os.Getenv("PENHOON_HTB_BURST_PROBE") != "1" || os.Geteuid() != 0 {
		return errors.New("probe requires explicitly authorized private network namespace")
	}
	link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "pui-burst0", TxQLen: 512}}
	if err := netlink.LinkAdd(link); err != nil {
		return err
	}
	defer netlink.LinkDel(link)
	address, err := netlink.ParseAddr("198.18.0.1/24")
	if err != nil {
		return err
	}
	if err := netlink.AddrAdd(link, address); err != nil {
		return err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return err
	}
	if err := netlink.NeighSet(&netlink.Neigh{LinkIndex: link.Attrs().Index, IP: net.ParseIP("198.18.0.2"), HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 2}, State: netlink.NUD_PERMANENT}); err != nil {
		return err
	}
	root := netlink.NewHtb(netlink.QdiscAttrs{LinkIndex: link.Attrs().Index, Handle: netlink.MakeHandle(1, 0), Parent: netlink.HANDLE_ROOT})
	root.Defcls = 1
	if err := netlink.QdiscAdd(root); err != nil {
		return err
	}
	connection, err := net.DialTimeout("udp4", "198.18.0.2:9000", time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	p := &probe{link: link, connection: connection}
	if err := p.create(false); err != nil {
		return err
	}
	full, err := p.measure()
	if err != nil {
		return err
	}
	if err := netlink.ClassDel(p.class); err != nil {
		return err
	}
	if err := p.create(true); err != nil {
		return err
	}
	empty, err := p.measure()
	if err != nil {
		return err
	}
	if err := netlink.QdiscDel(p.leaf); err != nil {
		return err
	}
	if err := netlink.QdiscReplace(p.leaf); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	zero := p.spec()
	zero.Buffer, zero.Cbuffer = 0, 0
	if err := netlink.ClassChange(zero); err != nil {
		return err
	}
	if err := netlink.ClassChange(p.spec()); err != nil {
		return err
	}
	retained, err := p.measure()
	if err != nil {
		return err
	}
	if err := netlink.ClassDel(p.class); err != nil {
		return err
	}
	started := time.Now()
	recreated := reading{}
	for range 128 {
		if err := p.create(true); err != nil {
			return err
		}
		if err := p.send(1); err != nil {
			return err
		}
		deadline := time.Now().Add(200 * time.Millisecond)
		for {
			seen, err := p.stats()
			if err != nil {
				return err
			}
			if seen.Packets != 0 {
				recreated.Bytes += seen.Bytes
				recreated.Packets += seen.Packets
				break
			}
			if time.Now().After(deadline) {
				return errors.New("empty class did not forward the probe packet")
			}
			time.Sleep(time.Millisecond)
		}
		if err := netlink.ClassDel(p.class); err != nil {
			return err
		}
	}
	elapsed := time.Since(started)
	recreated.ElapsedMilliseconds = elapsed.Milliseconds()
	recreated.EnvelopeBytes = probeBurst + packetEnvelope + uint64(elapsed.Seconds()*float64(probeRate/8))
	cooldown := time.Duration((probeBurst + packetEnvelope) * int64(time.Second) / (probeRate / 8))
	started = time.Now()
	cooled := reading{}
	cooledWithin := true
	for i := range 3 {
		if i != 0 {
			time.Sleep(cooldown)
		}
		if err := p.create(false); err != nil {
			return err
		}
		seen, err := p.measure()
		if err != nil {
			return err
		}
		cooled.Bytes += seen.Bytes
		cooled.Packets += seen.Packets
		elapsed = time.Since(started)
		cooled.ElapsedMilliseconds = elapsed.Milliseconds()
		cooled.EnvelopeBytes = probeBurst + packetEnvelope + uint64(elapsed.Seconds()*float64(probeRate/8))
		cooledWithin = cooledWithin && cooled.Bytes <= cooled.EnvelopeBytes
		if err := netlink.ClassDel(p.class); err != nil {
			return err
		}
	}
	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return err
	}
	result := map[string]any{"kernel": strings.TrimSpace(string(kernel)), "rateBitsPerSecond": probeRate, "burstBytes": probeBurst, "freshFull": full, "freshZeroThenGrow": empty, "retainedZeroThenGrow": retained, "repeatedEmptyRecreation": recreated, "freshZeroWithinPacketEnvelope": empty.Bytes <= empty.EnvelopeBytes, "retainedClassAccruedWhileUnused": retained.Bytes > retained.EnvelopeBytes, "recreationMintedBeyondGlobalEnvelope": recreated.Bytes > recreated.EnvelopeBytes}
	result["cooledFullRecreation"], result["cooldownMilliseconds"], result["cooldownWithinGlobalEnvelope"] = cooled, cooldown.Milliseconds(), cooledWithin
	return json.NewEncoder(os.Stdout).Encode(result)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
