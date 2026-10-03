package ipsc

import "testing"

// No accessor may panic on a voice message of any length, whatever its own
// length and kind bytes claim. IPSC has no login: these bytes come from
// anybody who can reach the port. Payload sliced to the end of the vocoder
// field after checking for five bytes fewer, and one datagram of 42 to 46
// body bytes stopped the server.
//
// To see it fail: put the check in Payload back to 21+2+VocoderLen.
func TestNoVoiceAccessorPanicsOnAShortFrame(t *testing.T) {
	kinds := []Kind{0x80, 0x81, 0x83, 0x84}
	frames := []byte{FrameHeader, FrameVoice, FrameTerminator, 0x00, 0xff}
	for _, kind := range kinds {
		for n := 0; n <= 80; n++ {
			for _, fk := range frames {
				for _, honest := range []bool{true, false} {
					b := make([]byte, n)
					for i := range b {
						b[i] = 0xff
					}
					if n > 25 {
						b[25] = fk
					}
					if n > 26 {
						b[26] = byte(n - 27)
						if !honest {
							b[26] = 0xff
						}
					}
					m := Message{Kind: kind, Body: b}
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Fatalf("kind %#x, %d body bytes, frame kind %#x, honest length %v: %v", byte(kind), n, fk, honest, r)
							}
						}()
						if _, vocoder, _, ok := m.Payload(); ok && len(vocoder) != VocoderLen {
							t.Fatalf("%d body bytes gave %d of vocoder", n, len(vocoder))
						}
						m.ColourCode()
						m.LinkControl()
						m.AsVoice()
						m.AsText()
						m.EmbeddedFragment()
						m.SuperframePosition()
						m.SlotBit()
					}()
				}
			}
		}
	}
}
