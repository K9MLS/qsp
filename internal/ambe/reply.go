package ambe

// Reply decoding.
//
// **These decode the four reply shapes this project has observed, and nothing
// else.** There is no general response parser here, deliberately: Table 32's
// response-length column says "none" for most fields while the chip
// acknowledges them with an identifier and a zero byte, so a parser built on
// that column would be built on a contradiction. Four shapes were captured on
// 2026-09-14 and all four are in testdata/ambe/observed-exchanges.hex. When a
// fifth is captured it gets a decoder; until then a packet that matches none
// of these is reported as unrecognised rather than guessed at.

// header checks the three bytes every packet starts with, and that the
// declared length matches what arrived. A short read is not a reply.
func header(pkt []byte, kind byte) ([]byte, bool) {
	if len(pkt) < 5 || pkt[0] != StartByte || pkt[3] != kind {
		return nil, false
	}
	if int(pkt[1])<<8|int(pkt[2]) != len(pkt)-4 {
		return nil, false
	}
	return pkt[4:], true
}

// AckedField reads an acknowledgement: a control packet carrying one field
// identifier and one status byte.
//
// Table 43 and its neighbours: the response echoes the field identifier
// followed by 0x00, and anything other than 0x00 indicates an error. Observed
// as `61 00 02 00 09 00` in reply to a PKT_RATET.
func AckedField(pkt []byte) (field byte, status byte, ok bool) {
	body, ok := header(pkt, TypeControl)
	if !ok || len(body) != 2 {
		return 0, 0, false
	}
	return body[0], body[1], true
}

// TextFromResponse reads a null-terminated string reply: PKT_PRODID or
// PKT_VERSTRING.
//
// Observed as `61 00 0b 00 30` then "AMBE3000F" and a terminator, and as
// `61 00 31 00 31` then the version string and a terminator.
//
// **The terminator and the field identifier are not part of the text.** The
// probe used to sieve the printable bytes out of the whole datagram, which
// rendered the product reply as "a0AMBE3000F" — the start byte as 'a' and the
// field identifier 0x30 as '0' — and the version reply with its length byte
// 0x31 inside the text as a stray '1'. Framing bytes read as payload is the
// same error as a byte read by eye.
func TextFromResponse(pkt []byte) (field byte, text string, ok bool) {
	body, ok := header(pkt, TypeControl)
	if !ok || len(body) < 2 {
		return 0, "", false
	}
	if body[0] != 0x30 && body[0] != 0x31 {
		return 0, "", false
	}
	data := body[1:]
	if data[len(data)-1] != 0x00 {
		return 0, "", false
	}
	return body[0], string(data[:len(data)-1]), true
}

// ChannelFrame is one compressed frame from the encoder.
type ChannelFrame struct {
	Bits int    // as the chip declared them
	Data []byte // the channel bits, packed eight to a byte
}

// Rate returns the bit rate the frame implies, in bits per second.
//
// A frame is 20 ms, so the rate is fifty times the bit count. This is how the
// rate was confirmed to have taken effect on 2026-09-14: the acknowledgement
// only says a field was received, while 72 bits in a frame says 3600 bps.
func (f ChannelFrame) Rate() int { return f.Bits * 50 }

// ChannelFrameFromResponse reads a channel packet containing a single CHAND
// field, which is what the chip returns for a speech packet.
//
// Observed as `61 00 0b 01 01 48` then nine bytes — 72 bits, 3600 bps, the
// DMR and P25 half-rate frame.
//
// The bit count is checked against the data that follows it, because a count
// that disagrees with its payload is the reply-direction form of the defect
// that wedged the chip, and it should be reported rather than trusted.
func ChannelFrameFromResponse(pkt []byte) (ChannelFrame, bool) {
	body, ok := header(pkt, TypeChannel)
	if !ok {
		return ChannelFrame{}, false
	}
	// A PKT_CHANNEL0 identifier may precede the CHAND field.
	if len(body) > 0 && body[0] == 0x40 {
		body = body[1:]
	}
	if len(body) < 2 || body[0] != 0x01 {
		return ChannelFrame{}, false
	}
	bits := int(body[1])
	data := body[2:]
	if bits < 40 || bits > 192 || len(data) != (bits+7)/8 {
		return ChannelFrame{}, false
	}
	return ChannelFrame{Bits: bits, Data: data}, true
}

// SpeechFromResponse reads a speech packet containing a single SPEECHD field,
// which is what the chip returns for a channel packet.
//
// **Not yet observed from hardware.** Table 99 and §6.8: the identifier, a
// sample count, then two bytes per sample most significant first. The count is
// checked against the data that follows it, and the bounds are skew control's
// 156 to 164. Everything else in this file decodes bytes this project has seen;
// this one decodes bytes it has reasoned about, and it says so.
func SpeechFromResponse(pkt []byte) ([]int16, bool) {
	body, ok := header(pkt, TypeSpeech)
	if !ok {
		return nil, false
	}
	if len(body) > 0 && body[0] == 0x40 {
		body = body[1:]
	}
	if len(body) < 2 || body[0] != 0x00 {
		return nil, false
	}
	count := int(body[1])
	data := body[2:]
	if count < 156 || count > 164 || len(data) != count*2 {
		return nil, false
	}
	out := make([]int16, count)
	for i := range out {
		out[i] = int16(uint16(data[i*2])<<8 | uint16(data[i*2+1]))
	}
	return out, true
}

// Mode names the operating mode and packet interface the IF_SELECT pins chose
// at boot, from the three configuration bytes a PKT_GETCFG returns.
//
// Table 9, printed page 31. The pins are CFG0 bits 0 to 2, and the operator's
// board reads 0x05 — IF_SELECT2 and IF_SELECT0 set — which is packet mode over
// the UART. That is the mode this project has been assuming throughout, now
// read off the pins rather than assumed.
func Mode(cfg [3]byte) string {
	switch cfg[0] & 0x07 {
	case 0x00:
		return "codec mode, SPI codec and UART packet interface"
	case 0x01:
		return "codec mode, SPI codec and parallel packet interface"
	case 0x02:
		return "codec mode, SPI codec and McBSP packet interface"
	case 0x03:
		return "codec mode, McBSP codec and UART packet interface"
	case 0x04:
		return "codec mode, McBSP codec and parallel packet interface"
	case 0x05:
		return "packet mode over the UART"
	case 0x06:
		return "packet mode over the parallel port"
	}
	return "packet mode over McBSP"
}

// CompandingEnabledIn reports whether the CP_ENABLE pin was set at boot,
// CFG0 bit 6 (Table 74).
//
// It matters for what a speech packet may contain: with companding disabled
// the samples are 16-bit linear, and with it enabled they are 8-bit a-law or
// µ-law, which halves the size of a SPEECHD field. The operator's board reads
// CFG0 0x05, so companding is off and 16-bit linear is right.
func CompandingEnabledIn(cfg [3]byte) bool {
	return cfg[0]&(1<<6) != 0
}

// RateControlWordIn returns the six RATE pins as the number they form,
// CFG1 bits 0 to 5 (Table 74).
//
// **Zero is the finding that matters.** The operator's board reads CFG1 0x00,
// so it boots with every RATE pin low and at whatever Table 116 gives for a
// control word of zero — which is not the DMR rate. Setting the rate with
// PKT_RATET is therefore a precondition for audio and not a refinement of it.
// The packet that wedged the dongle was an attempt to do a necessary thing by
// the wrong route.
func RateControlWordIn(cfg [3]byte) int {
	return int(cfg[1] & 0x3F)
}
