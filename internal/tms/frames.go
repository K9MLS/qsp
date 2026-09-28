package tms

import (
	"errors"
	"fmt"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// A text as a hotspot sends it.
//
// [Build] produces the datagram. Putting it on the air takes everything around
// it, and testdata/hbp/hbp-text-preambles.pcap shows exactly what: the
// operator's radio sent "K9MLS" to talkgroup 2 through a Pi-Star, and
// MMDVMHost forwarded twenty-two frames.
//
//   - **Sixteen preamble CSBKs, each in a stream of its own**, counting down
//     the bursts still to come — 21 on the first, 6 on the last.
//   - **A data header, then five Rate 1/2 blocks, sharing one stream.**
//   - A sequence number running 0 to 21 across all of it, not restarting at
//     each new stream.
//
// [Frames] composes the same twenty-two frames from a [Message], and
// frames_test.go holds it to that capture burst for burst. See
// [ADR-0067](../../docs/adr/ADR-0067-qsp-originates-a-text-message.md) phase 2.

// Preambles is how many preamble CSBKs open a composed text. Sixteen is what
// the hotspot sent, and the Motorola repeater's calibration capture carries
// the same number per message.
const Preambles = 16

// maxBlocks is the most data blocks a header can promise: its count is four
// bits wide and zero is not a count.
const maxBlocks = 0x0f

// MaxText is the most UTF-16 units a group text can carry: fifteen Rate 1/2
// blocks hold 180 octets, less the packet CRC's four, the IPv4 and UDP
// headers' twenty-eight, the TMS header's six and the leading CRLF's four,
// leaves 138 octets, which is 69 units. An ASCII character is one unit.
//
// This is the encoder's limit, not a radio's. ADR-0067 phase 4 measures what
// a radio displays, and a radio's own limit may well be lower.
const MaxText = 69

// ErrPrivateNotYet refuses a private text. A private text is a confirmed
// packet, and the radio receiving it answers with a response packet that QSP
// has no path for yet; ADR-0067 phase 3 is where that is built. A group text
// is unconfirmed and nobody answers it, which is why it goes first.
var ErrPrivateNotYet = errors.New("tms: only a group text can be composed for the air yet; a private one needs the acknowledgement path of ADR-0067 phase 3")

// ErrTooLong refuses a text that needs more blocks than a data header can
// promise.
var ErrTooLong = errors.New("tms: the text needs more than fifteen data blocks")

// Frames composes a group text as the Homebrew frames that carry it, in the
// order they are sent.
//
// colourCode is written into every burst and should be the receiving
// hotspot's own, from the configuration it logged in with. nextStream is
// called once for each stream the transmission needs — one per preamble and
// one for the message — and must not return the same ID twice. RepeaterID is
// left zero, because the sender stamps the peer it is sending to.
func Frames(m Message, slot hbp.Timeslot, colourCode uint8, nextStream func() hbp.StreamID) ([]hbp.Data, error) {
	switch {
	case !m.Group:
		return nil, ErrPrivateNotYet
	case colourCode > 15:
		return nil, fmt.Errorf("tms: colour code %d is outside 0 to 15", colourCode)
	case slot != hbp.Timeslot1 && slot != hbp.Timeslot2:
		return nil, fmt.Errorf("tms: %v is not a timeslot", slot)
	}

	payload, err := Build(m)
	if err != nil {
		return nil, fmt.Errorf("tms: composing frames: %w", err)
	}
	blocks := dmrfec.BlocksFor(len(payload), dmrfec.Rate12DataBytes)
	if blocks > maxBlocks {
		return nil, fmt.Errorf("%w: %d octets of datagram need %d", ErrTooLong, len(payload), blocks)
	}
	userData, err := dmrfec.JoinPacket(payload, blocks, dmrfec.Rate12DataBytes)
	if err != nil {
		return nil, fmt.Errorf("tms: composing frames: %w", err)
	}
	content, err := dmrfec.Rate12Blocks(userData)
	if err != nil {
		return nil, fmt.Errorf("tms: composing frames: %w", err)
	}
	header, err := dmrfec.BuildDataHeader(dmrfec.DataHeader{
		To:     m.To,
		From:   m.From,
		Group:  true,
		SAP:    dmrfec.SAPIPPacketData,
		Blocks: uint8(blocks),
		Pad:    uint8(dmrfec.PadOctets(len(payload), blocks, dmrfec.Rate12DataBytes)),
	})
	if err != nil {
		return nil, fmt.Errorf("tms: composing frames: %w", err)
	}

	out := make([]hbp.Data, 0, Preambles+1+blocks)
	add := func(dataType uint8, block []byte, stream hbp.StreamID) error {
		burst, err := dmrfec.BuildDataBurstFromBlock(colourCode, dataType, block)
		if err != nil {
			return fmt.Errorf("tms: coding data type %#x: %w", dataType, err)
		}
		d := hbp.Data{
			Sequence:  uint8(len(out)),
			SourceID:  m.From,
			TargetID:  m.To,
			Timeslot:  slot,
			CallType:  hbp.CallGroup,
			FrameType: hbp.FrameTypeSync,
			DataType:  dataType,
			StreamID:  stream,
		}
		copy(d.Payload[:], burst)
		out = append(out, d)
		return nil
	}

	for i := range Preambles {
		block, err := dmrfec.BuildPreamble(dmrfec.Preamble{
			BlocksToFollow: uint8(Preambles - 1 - i + 1 + blocks),
			To:             m.To,
			From:           m.From,
			Group:          true,
		})
		if err != nil {
			return nil, fmt.Errorf("tms: composing frames: %w", err)
		}
		if err := add(dmrfec.DataTypeCSBK, block, nextStream()); err != nil {
			return nil, err
		}
	}
	stream := nextStream()
	if err := add(dmrfec.DataTypeDataHeader, header, stream); err != nil {
		return nil, err
	}
	for _, block := range content {
		if err := add(dmrfec.DataTypeRate12, block, stream); err != nil {
			return nil, err
		}
	}
	return out, nil
}
