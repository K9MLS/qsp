package dmrfec_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// The six blocks of the captured "I can't talk right now..." transmission,
// eighteen octets each with the control pair last, exactly as they arrived.
var capturedBlocks = []string{
	"45000058b47100004011ba290c2fcdee00ac",
	"0c3025ad0fa70fa700441ad6003ae000032c",
	"84040d000a00490020006300610 06e0004c7",
	"2700740020007400610 06c006b00200 00647",
	"720069006700680 0740020006e006f0008ab",
	"77002e002e002e00000000009262 9ec20b72",
}

func blocksFixture(t *testing.T) [][]byte {
	t.Helper()
	out := make([][]byte, 0, len(capturedBlocks))
	for i, s := range capturedBlocks {
		b, err := hex.DecodeString(stripSpaces(s))
		if err != nil {
			t.Fatalf("fixture block %d: %v", i, err)
		}
		if len(b) != 18 {
			t.Fatalf("fixture block %d is %d octets, want 18", i, len(b))
		}
		out = append(out, b)
	}
	return out
}

// TestTheCapturedBlocksReassembleAndCutBackTheSame is the round trip that
// matters: every control pair, every serial number and every CRC-9 has to come
// back identical, because those are the octets a radio checks.
//
// To see it fail: change the serial shift in Rate34Blocks from nine to eight,
// or drop the `+ serial` from CRC9's message. Both produce blocks that look
// plausible and match nothing.
func TestTheCapturedBlocksReassembleAndCutBackTheSame(t *testing.T) {
	blocks := blocksFixture(t)

	userData, err := dmrfec.Rate34UserData(blocks, dmrfec.Rate34ControlLast)
	if err != nil {
		t.Fatalf("reassembling: %v", err)
	}
	if want := len(blocks) * dmrfec.Rate34DataBytes; len(userData) != want {
		t.Fatalf("user data is %d octets, want %d", len(userData), want)
	}

	again, err := dmrfec.Rate34Blocks(userData, dmrfec.Rate34ControlLast)
	if err != nil {
		t.Fatalf("segmenting: %v", err)
	}
	for i := range blocks {
		if !bytes.Equal(again[i], blocks[i]) {
			t.Errorf("block %d differs\n have %x\n want %x", i, again[i], blocks[i])
		}
	}
}

// TestTheBlockStreamSplitsWhereTheHeaderSaysItDoes ties the two files
// together: the pad count the data header carried is the pad
// [dmrfec.SplitPacket] finds, and the packet CRC is the last four octets.
func TestTheBlockStreamSplitsWhereTheHeaderSaysItDoes(t *testing.T) {
	blocks := blocksFixture(t)
	userData, err := dmrfec.Rate34UserData(blocks, dmrfec.Rate34ControlLast)
	if err != nil {
		t.Fatalf("reassembling: %v", err)
	}
	total := int(binary.BigEndian.Uint16(userData[2:4]))
	if total != 88 {
		t.Fatalf("IPv4 total length reads %d, want 88", total)
	}
	payload, pad, crc, err := dmrfec.SplitPacket(userData, total)
	if err != nil {
		t.Fatalf("splitting: %v", err)
	}
	if len(payload) != 88 {
		t.Errorf("payload %d octets, want 88", len(payload))
	}
	if len(pad) != 4 {
		t.Errorf("pad %d octets, want the 4 the header states", len(pad))
	}
	if crc != 0x92629ec2 {
		t.Errorf("packet CRC %#08x, want 0x92629ec2", crc)
	}
	if got := dmrfec.BlocksFor(total); got != len(blocks) {
		t.Errorf("BlocksFor(%d) is %d, and the transmission used %d", total, got, len(blocks))
	}

	joined, err := dmrfec.JoinPacket(payload, len(blocks), crc)
	if err != nil {
		t.Fatalf("joining: %v", err)
	}
	if !bytes.Equal(joined, userData) {
		t.Errorf("rejoined stream differs\n have %x\n want %x", joined, userData)
	}
}

// TestBothArrangementsRoundTrip covers the fork rate34.go names: IP Site
// Connect puts the control pair last and ETSI figure 8.8 draws it first, and
// which one goes out on air is the one reasoned step in the text path. Both
// have to work, because the answer may differ per destination.
func TestBothArrangementsRoundTrip(t *testing.T) {
	userData := bytes.Repeat([]byte{0xa5, 0x5a}, 24)
	for _, order := range []dmrfec.Rate34Order{dmrfec.Rate34ControlLast, dmrfec.Rate34ControlFirst} {
		t.Run(order.String(), func(t *testing.T) {
			blocks, err := dmrfec.Rate34Blocks(userData, order)
			if err != nil {
				t.Fatalf("segmenting: %v", err)
			}
			back, err := dmrfec.Rate34UserData(blocks, order)
			if err != nil {
				t.Fatalf("reassembling: %v", err)
			}
			if !bytes.Equal(back, userData) {
				t.Errorf("round trip changed the data\n have %x\n want %x", back, userData)
			}
			// And reading one arrangement as the other must be caught, or a
			// misconfigured destination silently produces rubbish.
			other := dmrfec.Rate34ControlFirst
			if order == dmrfec.Rate34ControlFirst {
				other = dmrfec.Rate34ControlLast
			}
			if _, err := dmrfec.Rate34UserData(blocks, other); err == nil {
				t.Errorf("blocks built %s read cleanly as %s", order, other)
			}
		})
	}
}

// TestABadBlockStreamIsRefused: the failures that would otherwise be silent.
func TestABadBlockStreamIsRefused(t *testing.T) {
	blocks := blocksFixture(t)

	t.Run("a missing block", func(t *testing.T) {
		gap := [][]byte{blocks[0], blocks[2], blocks[3]}
		if _, err := dmrfec.Rate34UserData(gap, dmrfec.Rate34ControlLast); err == nil {
			t.Error("reassembled a stream with block 1 missing")
		}
	})
	t.Run("blocks out of order", func(t *testing.T) {
		swapped := [][]byte{blocks[1], blocks[0], blocks[2]}
		if _, err := dmrfec.Rate34UserData(swapped, dmrfec.Rate34ControlLast); err == nil {
			t.Error("reassembled a stream whose serials run 1, 0, 2")
		}
	})
	t.Run("a corrupt crc9", func(t *testing.T) {
		bad := append([][]byte(nil), blocks...)
		bad[3] = append([]byte(nil), blocks[3]...)
		bad[3][5] ^= 0x01
		if _, err := dmrfec.Rate34UserData(bad, dmrfec.Rate34ControlLast); err == nil {
			t.Error("reassembled a stream with one octet flipped under a CRC-9")
		}
	})
	t.Run("a short block", func(t *testing.T) {
		bad := [][]byte{blocks[0][:17]}
		if _, err := dmrfec.Rate34UserData(bad, dmrfec.Rate34ControlLast); err == nil {
			t.Error("reassembled a seventeen-octet block")
		}
	})
	t.Run("no blocks", func(t *testing.T) {
		if _, err := dmrfec.Rate34UserData(nil, dmrfec.Rate34ControlLast); err == nil {
			t.Error("reassembled nothing into something")
		}
	})
	t.Run("user data that is not whole blocks", func(t *testing.T) {
		if _, err := dmrfec.Rate34Blocks(make([]byte, 17), dmrfec.Rate34ControlLast); err == nil {
			t.Error("cut 17 octets into 16-octet blocks")
		}
	})
	t.Run("an unknown arrangement", func(t *testing.T) {
		if _, err := dmrfec.Rate34Blocks(make([]byte, 16), dmrfec.Rate34OrderUnknown); err == nil {
			t.Error("built blocks in an arrangement nothing names")
		}
	})
}

// TestAPacketThatDoesNotFitIsRefused: JoinPacket must not silently truncate a
// payload that needs more blocks than it was given.
func TestAPacketThatDoesNotFitIsRefused(t *testing.T) {
	if _, err := dmrfec.JoinPacket(make([]byte, 90), 4, 0); err == nil {
		t.Error("joined 90 octets plus a CRC into four 16-octet blocks")
	}
	if _, _, _, err := dmrfec.SplitPacket(make([]byte, 16), 14); err == nil {
		t.Error("split a 14-octet payload and a 4-octet CRC out of 16 octets")
	}
}
