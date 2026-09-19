package p25link

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// radioPorts are the ports the captures in testdata exist to document: DMR's
// listener, IPSC, the P25 reflector and its neighbour, the gateway and the
// parrot, P25Gateway's remote commands, the MMDVM side, the vocoder, and USRP.
var radioPorts = map[uint16]string{
	62031: "DMR Homebrew", 62032: "DMR Homebrew, second",
	50000: "IPSC",
	41000: "P25 reflector", 41009: "P25 reflector, second",
	42010: "P25Gateway", 42011: "P25Gateway parrot", 42012: "P25Gateway to DMR",
	42020: "P25Gateway local", 32010: "MMDVM", 6074: "P25Gateway remote commands",
	2460: "AMBEserver", 32001: "USRP", 32002: "USRP, return",
}

// TestTheCapturesCarryOnlyRadioTraffic reads every capture in testdata and
// fails on a packet that is not radio.
//
// **The three P25 captures were taken with no capture filter**, and alongside
// the reflector conversation they held 159 mDNS packets naming household
// devices and twelve service types — AirPlay, HomeKit, SmartThings, SSH, a
// printer — plus Syncthing local discovery carrying a device ID and the
// operator's public address, Plex discovery, and SSDP with router UUIDs. 1,448
// of 1,876 packets in p25-register.pcap were somebody's house rather than a
// radio network, and every one of them would have been published.
//
// **A string scan would not have found it.** The 2026-09-16 scrub searched the
// tree for names, towns and addresses; `git grep` skips binary files, so the
// captures were never read, and even reading them would have found the
// addresses and left the device inventory. So this test is about ports rather
// than strings: a packet is radio or it is not, and what leaks is whatever
// happened to be on the wire.
//
// ARP, IPv6 and ICMP are allowed. Every capture in testdata has some, they
// carry no service inventory, and dropping them would rewrite twenty-three
// captures to no purpose.
func TestTheCapturesCarryOnlyRadioTraffic(t *testing.T) {
	captures := findCaptures(t)
	if len(captures) == 0 {
		t.Fatal("no captures found; this test would pass by finding nothing")
	}

	for _, path := range captures {
		t.Run(filepath.Base(path), func(t *testing.T) {
			foreign := map[string]int{}
			packets, offset := capturePackets(t, path)
			for _, pkt := range packets {
				flow, ok := udpOrTCP(pkt, offset)
				if !ok {
					continue // ARP, IPv6, ICMP: allowed above.
				}
				if _, ok := radioPorts[flow.src]; ok {
					continue
				}
				if _, ok := radioPorts[flow.dst]; ok {
					continue
				}
				foreign[fmt.Sprintf("%s %d->%d", flow.proto, flow.src, flow.dst)]++
			}
			if len(foreign) == 0 {
				return
			}
			kinds := make([]string, 0, len(foreign))
			for k, n := range foreign {
				kinds = append(kinds, fmt.Sprintf("%s (%d packets)", k, n))
			}
			sort.Strings(kinds)
			t.Errorf("%s carries traffic that is not radio: %s\n"+
				"filter it before committing: scripts/filter-capture.py in.pcap out.pcap <radio ports>",
				filepath.Base(path), strings.Join(kinds, ", "))
		})
	}
}

// TestTheCapturesNameNoHostAndNoIdentity is the second half, for what survives
// inside an allowed packet: a capture on a radio port can still carry an
// identity if it was on that port, and a reader looking only at ports would
// miss it.
func TestTheCapturesNameNoHostAndNoIdentity(t *testing.T) {
	patterns := map[string]*regexp.Regexp{
		"an mDNS name":          regexp.MustCompile(`\x05local\x00`),
		"a Syncthing device ID": regexp.MustCompile(`[A-Z0-9]{7}-[A-Z0-9]{7}-[A-Z0-9]{7}`),
		"a UPnP device UUID":    regexp.MustCompile(`uuid:[0-9a-f]{8}-[0-9a-f]{4}`),
		"a relay URL":           regexp.MustCompile(`relay://`),
	}

	for _, path := range findCaptures(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			for what, re := range patterns {
				if n := len(re.FindAll(b, -1)); n > 0 {
					t.Errorf("%s contains %s (%d times); it identifies a device or a household rather than a radio",
						filepath.Base(path), what, n)
				}
			}
		})
	}
}

func findCaptures(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{"hbp", "ipsc", "p25"} {
		matches, err := filepath.Glob(filepath.Join("../../testdata", dir, "*.pcap"))
		if err != nil {
			t.Fatalf("globbing %s: %v", dir, err)
		}
		out = append(out, matches...)
	}
	sort.Strings(out)
	return out
}

// capturePackets returns each packet's link-layer bytes, and the offset of the
// IP header within them, which depends on the file's link type.
func capturePackets(t *testing.T, path string) ([][]byte, int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if len(data) < 24 {
		t.Fatalf("%s: too short to be a pcap", path)
	}

	var order binary.ByteOrder
	switch string(data[:4]) {
	case "\xd4\xc3\xb2\xa1", "\x4d\x3c\xb2\xa1":
		order = binary.LittleEndian
	case "\xa1\xb2\xc3\xd4", "\xa1\xb2\x3c\x4d":
		order = binary.BigEndian
	default:
		t.Fatalf("%s: not a pcap (magic %x)", path, data[:4])
	}

	link := order.Uint32(data[20:24])
	offset, ok := linkOffsets[link]
	if !ok {
		t.Fatalf("%s: link type %d is not one this test understands", path, link)
	}

	var out [][]byte
	for off := 24; off+16 <= len(data); {
		caplen := int(order.Uint32(data[off+8 : off+12]))
		off += 16
		if off+caplen > len(data) {
			t.Fatalf("%s: a packet record runs past the end of the file", path)
		}
		out = append(out, data[off:off+caplen])
		off += caplen
	}
	return out, offset
}

// linkOffsets maps a pcap link type to where the IP header starts. 276 is
// LINUX_SLL2, which `tcpdump -i any` writes and which every capture here uses.
var linkOffsets = map[uint32]int{1: 14, 113: 16, 276: 20}

type flow struct {
	proto    string
	src, dst uint16
}

// udpOrTCP returns the ports of a UDP or TCP packet, and false for anything
// else — ARP, IPv6, ICMP, or a truncated packet. off is where the IP header
// starts, from capturePackets.
func udpOrTCP(pkt []byte, off int) (flow, bool) {
	// The EtherType, whose position depends on the link type.
	var etherType int
	switch off {
	case 14:
		etherType = 12
	case 16:
		etherType = 14
	default:
		etherType = 0
	}
	if len(pkt) < off || len(pkt) < etherType+2 {
		return flow{}, false
	}
	if binary.BigEndian.Uint16(pkt[etherType:etherType+2]) != 0x0800 {
		return flow{}, false
	}
	ip := pkt[off:]
	if len(ip) < 20 || ip[0]>>4 != 4 {
		return flow{}, false
	}
	var proto string
	switch ip[9] {
	case 6:
		proto = "tcp"
	case 17:
		proto = "udp"
	default:
		return flow{}, false
	}
	l4 := ip[int(ip[0]&0xF)*4:]
	if len(l4) < 4 {
		return flow{}, false
	}
	return flow{proto, binary.BigEndian.Uint16(l4[0:2]), binary.BigEndian.Uint16(l4[2:4])}, true
}
