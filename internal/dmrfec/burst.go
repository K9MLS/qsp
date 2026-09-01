package dmrfec

// Burst sizes, in bits, from ETSI TS 102 361-1.
const (
	// BurstBits is a DMR traffic burst: 264 bits, 27.5 ms of a 30 ms slot.
	BurstBits = 264
	// BurstBytes is the same burst as the Homebrew protocol carries it.
	BurstBytes = 33
	// SyncOffset is where the 48-bit synchronisation or embedded signalling
	// field begins.
	//
	// **It sits in the middle rather than at the front**, which is why the
	// vocoder payload is two halves rather than one run. The standard puts it
	// there to support reverse-channel signalling; the consequence for anyone
	// reading a burst is that naive slicing silently mixes sync bits into the
	// audio.
	SyncOffset = 108
	// SyncBits is the length of that field.
	SyncBits = 48
)

// VoiceSyncBS is the synchronisation pattern a base station puts in the first
// burst of every superframe.
//
// Confirmed in the captures: it appears in exactly one burst in six of
// testdata/hbp/hbp-voice-session.pcap, which is what a 360 ms superframe of six
// 60 ms frames should produce.
const VoiceSyncBS = 0x755FD7DF75F7

// BurstBitsFrom expands a 33-byte burst into 264 bits, most significant first.
func BurstBitsFrom(burst []byte) []byte {
	out := make([]byte, 0, BurstBits)
	for _, b := range burst {
		for i := 7; i >= 0; i-- {
			out = append(out, b>>uint(i)&1)
		}
	}
	return out
}

// BurstBytesFrom packs 264 bits back into 33 bytes.
func BurstBytesFrom(bits []byte) []byte {
	out := make([]byte, BurstBytes)
	for i, b := range bits {
		if b&1 == 1 {
			out[i/8] |= 1 << uint(7-i%8)
		}
	}
	return out
}

// VocoderFrames pulls the three 72-bit vocoder frames out of a burst.
//
// The payload is the 108 bits before the sync field and the 108 after it,
// concatenated: 216 bits, three frames of 72.
func VocoderFrames(burst []byte) ([][]byte, bool) {
	if len(burst) != BurstBytes {
		return nil, false
	}
	b := BurstBitsFrom(burst)
	payload := make([]byte, 0, ProtectedBits*FramesPerBurst)
	payload = append(payload, b[:SyncOffset]...)
	payload = append(payload, b[SyncOffset+SyncBits:]...)

	out := make([][]byte, FramesPerBurst)
	for i := range out {
		out[i] = payload[i*ProtectedBits : (i+1)*ProtectedBits]
	}
	return out, true
}

// AssembleBurst builds a 33-byte burst from three vocoder frames and the 48-bit
// field that belongs in the middle — a synchronisation pattern on the first
// burst of a superframe, embedded signalling on the others.
func AssembleBurst(frames [][]byte, middle uint64) ([]byte, bool) {
	if len(frames) != FramesPerBurst {
		return nil, false
	}
	payload := make([]byte, 0, ProtectedBits*FramesPerBurst)
	for _, f := range frames {
		if len(f) != ProtectedBits {
			return nil, false
		}
		payload = append(payload, f...)
	}

	bits := make([]byte, 0, BurstBits)
	bits = append(bits, payload[:SyncOffset]...)
	for i := SyncBits - 1; i >= 0; i-- {
		bits = append(bits, byte(middle>>uint(i))&1)
	}
	bits = append(bits, payload[SyncOffset:]...)
	return BurstBytesFrom(bits), true
}

// Middle returns the 48 bits between the two payload halves.
func Middle(burst []byte) (uint64, bool) {
	if len(burst) != BurstBytes {
		return 0, false
	}
	b := BurstBitsFrom(burst)
	var v uint64
	for _, x := range b[SyncOffset : SyncOffset+SyncBits] {
		v = v<<1 | uint64(x&1)
	}
	return v, true
}
