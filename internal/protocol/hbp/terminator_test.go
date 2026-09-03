package hbp_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestOnlyATerminatorIsATerminator pins a distinction that was absent until
// text messages arrived.
//
// `FrameTypeSync` means "a data burst", and a voice LC header, a terminator and
// a text are all data bursts. Testing the frame type alone answered true for
// all three, which dropped every text message on the outbound path and would
// have cut an over short had a hotspot sent a header mid-transmission.
func TestOnlyATerminatorIsATerminator(t *testing.T) {
	for _, tc := range []struct {
		name         string
		frame        hbp.Data
		want         bool
		whyItMatters string
	}{
		{
			name:  "a terminator",
			frame: hbp.Data{FrameType: hbp.FrameTypeSync, DataType: 0x2},
			want:  true,
		},
		{
			name:  "a voice LC header",
			frame: hbp.Data{FrameType: hbp.FrameTypeSync, DataType: 0x1},
			want:  false,
			whyItMatters: "reading a header as a terminator would close a " +
				"transmission that had just opened",
		},
		{
			name:  "a text message CSBK",
			frame: hbp.Data{FrameType: hbp.FrameTypeSync, DataType: 0x3},
			want:  false,
			whyItMatters: "reading a text as a terminator drops it, which is " +
				"exactly what happened before ADR-0045",
		},
		{
			name:  "a text message Rate 1/2 block",
			frame: hbp.Data{FrameType: hbp.FrameTypeSync, DataType: 0x7},
			want:  false,
		},
		{
			name:  "a voice frame",
			frame: hbp.Data{FrameType: hbp.FrameTypeVoice},
			want:  false,
		},
		{
			name:  "a voice sync frame",
			frame: hbp.Data{FrameType: hbp.FrameTypeVoiceSync},
			want:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.frame.IsTerminator(); got != tc.want {
				msg := tc.whyItMatters
				if msg == "" {
					msg = "the frame type and data type together say otherwise"
				}
				t.Errorf("IsTerminator is %v, want %v: %s", got, tc.want, msg)
			}
		})
	}
}
