package peers_test

import (
	"encoding/binary"
	"fmt"
	"os"
)

// readCaptureRaw extracts UDP payloads from a LINUX_SLL2 pcap.
//
// It duplicates the reader in internal/protocol/hbp's tests rather than sharing
// it: a test helper exported for reuse becomes API, and this is fifty lines
// that neither package should depend on the other for.
func readCaptureRaw(path string) ([][]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read fixture %s: %w", path, err)
	}
	if len(raw) < 24 {
		return nil, fmt.Errorf("fixture %s is too small to be a pcap", path)
	}
	if binary.LittleEndian.Uint32(raw[20:24]) != 276 {
		return nil, fmt.Errorf("fixture %s is not LINUX_SLL2", path)
	}

	var out [][]byte
	off := 24
	for off+16 <= len(raw) {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if incl < 0 || off+incl > len(raw) {
			return nil, fmt.Errorf("fixture %s: truncated record", path)
		}
		rec := raw[off : off+incl]
		off += incl

		const sll2 = 20
		if len(rec) < sll2+20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[sll2:]
		ihl := int(ip[0]&0x0F) * 4
		if ihl < 20 || len(ip) < ihl+8 || ip[9] != 17 {
			continue
		}
		udp := ip[ihl:]
		length := int(binary.BigEndian.Uint16(udp[4:6]))
		if length < 8 || length > len(udp) {
			continue
		}
		payload := make([]byte, length-8)
		copy(payload, udp[8:length])
		out = append(out, payload)
	}
	return out, nil
}
