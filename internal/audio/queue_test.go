package audio

import (
	"strings"
	"testing"
)

// What comes out of a queue of four for what went in. K is a keyup, R a
// release, and a digit is an audio frame whose first sample is that digit.
//
// Break it: refuse a frame when the queue is full, as the channel this
// replaced did, and every row past the first three fails; give up the oldest
// frame whatever it is, and the rows that end an over lose their keyup.
func TestWhatAFullQueueGivesUp(t *testing.T) {
	tests := []struct {
		name string
		in   string
		out  string
		lost uint64
	}{
		{"nothing", "", "", 0},
		{"fewer than it holds, in order", "K12R", "K12R", 0},
		{"exactly what it holds", "K123", "K123", 0},
		{"one more audio frame costs the oldest audio", "K1234", "K234", 1},
		{"a release into a full queue costs audio, not the release", "K123R", "K23R", 1},
		{"the keyup ahead of the audio is kept", "K12345R", "K45R", 3},
		{"the newest audio is what stays", "123456789", "6789", 5},
		{"an over that ended is not lost to the next one's audio", "K1RK23", "KRK3", 2},
		{"nothing but signals gives up the oldest", "KRKRKR", "KRKR", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := NewQueue(4)
			for _, c := range tc.in {
				switch c {
				case 'K':
					q.Push(Frame{PTT: true})
				case 'R':
					q.Push(Frame{})
				default:
					q.Push(Frame{PTT: true, Samples: []int16{int16(c - '0')}})
				}
			}
			var out strings.Builder
			for {
				f, ok := q.Pop()
				if !ok {
					break
				}
				switch {
				case !f.PTT:
					out.WriteByte('R')
				case len(f.Samples) == 0:
					out.WriteByte('K')
				default:
					out.WriteByte(byte('0' + f.Samples[0]))
				}
			}
			if out.String() != tc.out {
				t.Errorf("%q came out as %q, want %q", tc.in, out.String(), tc.out)
			}
			if q.Lost() != tc.lost {
				t.Errorf("%d frames lost, want %d", q.Lost(), tc.lost)
			}
		})
	}
}

// A reader that waits on Ready and takes one frame each time sees every
// frame: a wake is not lost when several frames arrive before the reader
// looks.
//
// Break it: have Pop not wake again while frames remain.
func TestAReaderTakingOneAtATimeSeesEveryFrame(t *testing.T) {
	q := NewQueue(8)
	for range 5 {
		q.Push(Frame{PTT: true, Samples: []int16{1}})
	}
	for i := range 5 {
		select {
		case <-q.Ready():
		default:
			t.Fatalf("not ready with %d frames still held", 5-i)
		}
		if _, ok := q.Pop(); !ok {
			t.Fatalf("ready, and nothing to take, with %d frames still held", 5-i)
		}
	}
	select {
	case <-q.Ready():
		t.Error("ready with nothing held")
	default:
	}
}

func TestDiscardEmptiesTheQueue(t *testing.T) {
	q := NewQueue(8)
	for range 3 {
		q.Push(Frame{PTT: true, Samples: []int16{1}})
	}
	if n := q.Discard(); n != 3 {
		t.Errorf("Discard reported %d, want 3", n)
	}
	if _, ok := q.Pop(); ok {
		t.Error("a frame survived Discard")
	}
	if q.Lost() != 0 {
		t.Error("frames discarded on purpose were counted as lost to a full queue")
	}
}
