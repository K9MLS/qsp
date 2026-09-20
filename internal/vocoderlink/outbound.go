package vocoderlink

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"

	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// GeneratedColourCode is the colour code on every burst this package builds.
//
// **It is a placeholder, and the reason it may be one is evidence, not
// convenience.** A colour code belongs to each receiving repeater, never to a
// transcoder: one bridge can feed a repeater on colour code 1 and another
// across a state line on 7. On 2026-09-16 the operator confirmed that the four
// production hotspots run different colour codes in different places and hear
// each other through QSP, which forwards Homebrew bursts with the sender's
// colour code untouched — so the Homebrew side rebuilds it on receipt. The
// Motorola side is stamped per repeater by ADR-0042. Nothing downstream reads
// this value, which is the only condition under which a constant is honest.
const GeneratedColourCode = 1

// outbound is a transmission being built from USRP audio.
type outbound struct {
	// chip is nil for a keyup that was refused; the rest of it is absorbed
	// until its release.
	chip     Chip
	stream   hbp.StreamID
	sequence uint8
	// position is the next burst's place in the six-burst superframe.
	position int
	// frames holds encoded 72-bit frames waiting to fill a burst of three.
	frames  [][]byte
	lc      []byte
	middles [dmrfec.EmbeddedLCBursts]uint64
	// superframe counts complete superframes sent, which selects what rides
	// in the embedded signalling: the Link Control, then one Talker Alias
	// PDU per superframe, then round again. See middlesFor.
	superframe int
	lastSeen   time.Time
}

// middlesFor returns the four embedded-signalling middles for the superframe
// being sent.
//
// **The Link Control comes first and comes back.** A radio that joined late,
// or missed a burst to a fade, needs the LC again rather than a cycle of alias
// blocks it cannot attribute to anybody; the standard puts no obligation on a
// transmitter to send an alias at all, so the LC is what repeats and the alias
// is what fills the gaps between repeats.
//
// A transmission shorter than the cycle carries part of it, which is inherent:
// a superframe is 360 ms, so a 31-character alias is a second or so of talking
// before the text is complete. Yesterday's production log has a 142 ms Zello
// transmission -- it will carry the LC and nothing else, which is exactly what
// it carries today.
func (c *Channel) middlesFor(tx *outbound) [dmrfec.EmbeddedLCBursts]uint64 {
	if len(c.aliasMiddles) == 0 {
		return tx.middles
	}
	switch at := tx.superframe % (1 + len(c.aliasMiddles)); at {
	case 0:
		return tx.middles
	default:
		return c.aliasMiddles[at-1]
	}
}

// handleUSRP takes one frame from the far side.
func (c *Channel) handleUSRP(tx *outbound, cur *call, f audio.Frame, now time.Time) *outbound {
	if c.deliver == nil {
		return tx
	}
	if !f.PTT {
		c.endOutbound(tx, "")
		return nil
	}
	if tx == nil {
		tx = c.startOutbound(cur, now)
	}
	tx.lastSeen = now
	if tx.chip == nil || len(f.Samples) == 0 {
		return tx
	}
	if err := c.encodeInto(tx, f.Samples); err != nil {
		c.failOutbound(tx, err.Error())
		return tx
	}
	if len(tx.frames) == dmrfec.FramesPerBurst {
		c.emitVoice(tx)
	}
	return tx
}

// startOutbound opens a transmission: take the chip, then send the header.
func (c *Channel) startOutbound(cur *call, now time.Time) *outbound {
	tx := &outbound{lastSeen: now}

	// **The network is already talking.** A DMR call holding the chip means
	// somebody on a radio has the channel, and a Zello user keying over them
	// is refused exactly as a second radio would be.
	if cur != nil && cur.chip != nil {
		c.txRefused.Add(1)
		c.note("audio from USRP arrived while a DMR call held the vocoder")
		return tx
	}
	chip := c.chip()
	if chip == nil {
		c.txRefused.Add(1)
		c.note("audio from USRP arrived and the vocoder is not reachable")
		return tx
	}
	if bits, ok := ambe.FrameBitsForRate(chip.Rate()); !ok || bits != dmrfec.ProtectedBits {
		c.txRefused.Add(1)
		c.note(fmt.Sprintf("the vocoder is at rate index %d, which does not carry DMR's %d-bit frames",
			chip.Rate(), dmrfec.ProtectedBits))
		return tx
	}
	if err := chip.Acquire(ambe.Holder{Talkgroup: c.talkgroup, Source: c.radioID,
		Reason: "USRP to DMR"}); err != nil {
		c.txRefused.Add(1)
		c.note(fmt.Sprintf("the vocoder refused audio from USRP: %v", err))
		return tx
	}

	lc := dmrfec.LinkControlFor(c.talkgroup, c.radioID, false)
	middles, err := dmrfec.EmbeddedLCMiddles(lc, GeneratedColourCode)
	if err != nil {
		chip.Release()
		c.txRefused.Add(1)
		c.note(fmt.Sprintf("cannot build embedded Link Control: %v", err))
		return tx
	}
	var id [4]byte
	_, _ = rand.Read(id[:])
	// **A fresh stream ID per transmission**, so deduplication on a linked
	// server sees a new call rather than the tail of the last one. Zero is
	// avoided because nothing else ever sends it.
	stream := hbp.StreamID(binary.BigEndian.Uint32(id[:]) | 1)

	tx.chip, tx.lc, tx.middles, tx.stream = chip, lc, middles, stream
	header, err := dmrfec.BuildDataBurst(GeneratedColourCode, dmrfec.DataTypeVoiceLCHeader, lc)
	if err != nil {
		c.failOutbound(tx, fmt.Sprintf("building the voice header: %v", err))
		return tx
	}
	c.txCalls.Add(1)
	c.setHolder(fmt.Sprintf("audio from USRP as radio %d on talkgroup %d", c.radioID, c.talkgroup))
	c.log.Info("transmitting audio from USRP",
		slog.Uint64("radio", uint64(c.radioID)),
		slog.Uint64("talkgroup", uint64(c.talkgroup)))
	c.send(tx, hbp.FrameTypeSync, dmrfec.DataTypeVoiceLCHeader, header)
	return tx
}

// encodeInto encodes 20 ms of PCM and queues the frame.
func (c *Channel) encodeInto(tx *outbound, samples []int16) error {
	frame, err := tx.chip.Encode(c.toDMR.Apply(samples))
	if err != nil {
		return fmt.Errorf("encoding audio from USRP: %w", err)
	}
	if frame.Bits != dmrfec.ProtectedBits {
		return fmt.Errorf("the vocoder encoded a %d-bit frame, want %d; the rate is not DMR's",
			frame.Bits, dmrfec.ProtectedBits)
	}
	bits := unpackBits(frame.Data, dmrfec.ProtectedBits)
	// **Checked against DMR's own FEC before it goes on a network.** A chip
	// producing frames in a layout radios do not use would otherwise present
	// as a repeater that keys up and plays nothing; this counter says which.
	//
	// **Any correction counts, not only a failure.** Decode reports failure
	// only when both Golay blocks are beyond repair, so nearly any 72 bits
	// "decode". A frame the chip has just encoded has had no channel to be
	// damaged by, and needs no correction at all if its layout is DMR's.
	if _, corrected, ok := dmrfec.Decode(bits); !ok || corrected > 0 {
		c.txBadFEC.Add(1)
	}
	tx.frames = append(tx.frames, bits)
	return nil
}

// emitVoice sends the three queued frames as the next burst of the superframe.
func (c *Channel) emitVoice(tx *outbound) {
	var middle uint64
	switch {
	case tx.position == 0:
		middle = dmrfec.VoiceSyncBS
	case tx.position <= dmrfec.EmbeddedLCBursts:
		middle = c.middlesFor(tx)[tx.position-1]
	default:
		// Burst F: the single-LCSS EMB and no fragment, as every capture shows.
		m, err := dmrfec.MiddleForPosition(tx.position, GeneratedColourCode, 0)
		if err != nil {
			c.failOutbound(tx, fmt.Sprintf("building burst F: %v", err))
			return
		}
		middle = m
	}
	burst, ok := dmrfec.AssembleBurst(tx.frames, middle)
	if !ok {
		c.failOutbound(tx, "assembling a voice burst")
		return
	}
	frameType := hbp.FrameTypeVoice
	if tx.position == 0 {
		frameType = hbp.FrameTypeVoiceSync
	}
	c.send(tx, frameType, uint8(tx.position), burst)
	tx.frames = tx.frames[:0]
	tx.position = (tx.position + 1) % dmrfec.SuperframeBursts
	if tx.position == 0 {
		tx.superframe++
	}
}

// endOutbound finishes a transmission: flush, terminate, release.
//
// **The last partial burst is padded with silence, not dropped.** A
// transmission is rarely a multiple of three frames, and dropping the remainder
// clips the last word of every over.
func (c *Channel) endOutbound(tx *outbound, why string) {
	if tx == nil || tx.chip == nil {
		return
	}
	if len(tx.frames) > 0 {
		silence := make([]int16, audio.SamplesPerFrame)
		for len(tx.frames) < dmrfec.FramesPerBurst {
			if err := c.encodeInto(tx, silence); err != nil {
				tx.frames = tx.frames[:0]
				break
			}
		}
		if len(tx.frames) == dmrfec.FramesPerBurst {
			c.emitVoice(tx)
		}
	}
	if tx.chip == nil {
		// emitVoice failed and already terminated.
		return
	}
	c.terminate(tx)
	if why != "" {
		c.note(why)
	}
}

// failOutbound ends a transmission that cannot continue, still with a
// terminator: repeaters hang keyed without one.
func (c *Channel) failOutbound(tx *outbound, why string) {
	c.failed.Add(1)
	c.note(why)
	if tx.chip != nil {
		c.terminate(tx)
	}
}

func (c *Channel) terminate(tx *outbound) {
	if tx.lc != nil {
		if term, err := dmrfec.BuildDataBurst(GeneratedColourCode, dmrfec.DataTypeTerminatorWithLC, tx.lc); err == nil {
			c.send(tx, hbp.FrameTypeSync, dmrfec.DataTypeTerminatorWithLC, term)
		} else {
			c.note(fmt.Sprintf("cannot build a terminator: %v", err))
		}
	}
	tx.chip.Release()
	tx.chip = nil
	tx.frames = nil
	c.setHolder("")
}

func (c *Channel) send(tx *outbound, frameType hbp.FrameType, dataType uint8, burst []byte) {
	d := hbp.Data{
		Sequence:  tx.sequence,
		SourceID:  c.radioID,
		TargetID:  c.talkgroup,
		Timeslot:  c.timeslot,
		CallType:  hbp.CallGroup,
		FrameType: frameType,
		DataType:  dataType,
		StreamID:  tx.stream,
	}
	copy(d.Payload[:], burst)
	tx.sequence++
	c.txBursts.Add(1)
	c.deliver(d)
}

// unpackBits turns bytes into one-bit-per-byte values, most significant first.
func unpackBits(data []byte, n int) []byte {
	out := make([]byte, n)
	for i := range n {
		if i/8 < len(data) && data[i/8]>>(7-i%8)&1 == 1 {
			out[i] = 1
		}
	}
	return out
}
