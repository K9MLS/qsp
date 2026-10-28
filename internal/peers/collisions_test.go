package peers

import (
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/routing"
)

// TestAKeyOverIsOneNoteWithItsFramesCounted is the record COLLISIONS is
// explained from: one note per run, not per frame.
//
// 2026-10-27: 748 refused frames were three key-overs, so the record has to
// say three, with when, and add up to the number beside it.
//
// To see it fail:
//   - in withCollision, append without looking for a run: every frame is a
//     note, and the counts are all wrong;
//   - compare with < 0 rather than <= collisionRunGap: likewise;
//   - drop the Summary test: two talkers at once become one run.
func TestAKeyOverIsOneNoteWithItsFramesCounted(t *testing.T) {
	const (
		overZello = `peer 3121380 keyed up while transcoder "zello" was carrying TG 2`
		overLink  = "peer 3132910 keyed up while link BCARA was carrying TG 9"
	)
	at := time.Date(2026, 10, 26, 21, 36, 0, 0, time.UTC)
	frame := 60 * time.Millisecond
	type refusal struct {
		summary string
		after   time.Duration
	}
	run := func(summary string, from time.Duration, frames int) []refusal {
		var out []refusal
		for i := range frames {
			out = append(out, refusal{summary, from + time.Duration(i)*frame})
		}
		return out
	}
	join := func(parts ...[]refusal) []refusal {
		var out []refusal
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	for _, tc := range []struct {
		name     string
		frames   []refusal
		refusals []uint64 // per note, oldest first
	}{
		{"one key-over of fifty frames", run(overZello, 0, 50), []uint64{50}},
		{"three key-overs a minute apart, which is 2026-10-26",
			join(run(overZello, 0, 30), run(overZello, time.Minute, 20), run(overZello, 2*time.Minute, 10)),
			[]uint64{30, 20, 10}},
		{"a pause shorter than the gap is the same over",
			join(run(overZello, 0, 10), run(overZello, 10*frame+2*time.Second, 10)), []uint64{20}},
		{"a pause longer than the gap is another",
			join(run(overZello, 0, 10), run(overZello, 10*frame+4*time.Second, 10)), []uint64{10, 10}},
		{"two at once, interleaved, stay two", func() []refusal {
			var out []refusal
			for i := range 10 {
				d := time.Duration(i) * frame
				out = append(out, refusal{overZello, d}, refusal{overLink, d})
			}
			return out
		}(), []uint64{10, 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var notes []CollisionNote
			for _, r := range tc.frames {
				notes = withCollision(notes, r.summary, true, at.Add(r.after))
			}
			got := make([]uint64, len(notes))
			for i, n := range notes {
				got[i] = n.Refusals
			}
			if len(got) != len(tc.refusals) {
				t.Fatalf("%d notes %v, want %d %v", len(got), got, len(tc.refusals), tc.refusals)
			}
			for i := range got {
				if got[i] != tc.refusals[i] {
					t.Errorf("note %d counts %d, want %d", i, got[i], tc.refusals[i])
				}
			}
			last := notes[len(notes)-1]
			if !last.At.Equal(at.Add(tc.frames[len(tc.frames)-1].after)) {
				t.Errorf("the last note ends at %s, not at its last frame", last.At)
			}
		})
	}
}

// TestTheCollisionRecordIsBounded: a peer keying over all evening must not
// grow it.
//
// To see it fail: remove the slices.Delete in withCollision.
func TestTheCollisionRecordIsBounded(t *testing.T) {
	var notes []CollisionNote
	at := time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC)
	for i := range 3 * maxCollisionNotes {
		notes = withCollision(notes, "over", true, at.Add(time.Duration(i)*time.Minute))
	}
	if len(notes) != maxCollisionNotes {
		t.Fatalf("%d notes kept, want %d", len(notes), maxCollisionNotes)
	}
	if want := at.Add(time.Duration(3*maxCollisionNotes-1) * time.Minute); !notes[len(notes)-1].At.Equal(want) {
		t.Errorf("the newest note is %s, want %s: the oldest should go", notes[len(notes)-1].At, want)
	}
}

// TestACollisionSaysWhoTalkedOverWhom: the sentence the console shows.
//
// To see it fail: swap Refused and Holder in collisionSummary.
func TestACollisionSaysWhoTalkedOverWhom(t *testing.T) {
	for _, tc := range []struct {
		name    string
		drop    routing.Drop
		want    string
		keyOver bool
	}{
		{"a key-over", routing.Drop{
			Reason: "timeslot already carrying TG 2 from transcoder \"zello\"",
			Contention: &routing.Contention{
				Holder: `transcoder "zello"`, Refused: "peer 3121380", Talkgroup: 2,
			},
		}, `peer 3121380 keyed up while transcoder "zello" was carrying TG 2`, true},
		{"any other refusal says where and why", routing.Drop{
			To:     routing.Endpoint{Peer: 3132910, Talkgroup: 9, Timeslot: 2},
			Reason: "talkgroup 9 is not permitted",
		}, "not sent to 3132910/TG9/TS2: talkgroup 9 is not permitted", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, keyOver := collisionSummary(tc.drop)
			if !strings.HasPrefix(got, tc.want) || keyOver != tc.keyOver {
				t.Errorf("%q key-over %v, want %q %v", got, keyOver, tc.want, tc.keyOver)
			}
		})
	}
}

// TestTheRecordAddsUpToTheCounter: what the console says beside COLLISIONS
// accounts for every frame the number counted, and for nothing it did not.
//
// To see it fail: put `l.collided.Add(1)` back in deliver in place of
// countCollision, and the record is empty while the counter reads 51.
func TestTheRecordAddsUpToTheCounter(t *testing.T) {
	l, _ := journal()
	over := routing.Drop{
		To:     routing.Endpoint{Peer: 3132910, Talkgroup: 9, Timeslot: hbp.Timeslot2},
		Reason: `timeslot already carrying TG 2 from transcoder "zello"`,
		Contention: &routing.Contention{
			Holder: `transcoder "zello"`, Refused: "peer 3121380", Talkgroup: 2,
		},
	}
	policy := routing.Drop{
		To:            routing.Endpoint{Peer: 3132910, Talkgroup: 2, Timeslot: hbp.Timeslot2},
		Reason:        "has not opted in",
		NotACollision: true,
	}
	for range 50 {
		l.deliver(0, routing.Result{Drops: []routing.Drop{over, policy}})
	}
	l.deliver(0, routing.Result{Drops: []routing.Drop{{
		To:     routing.Endpoint{Peer: 3132910, Talkgroup: 11, Timeslot: hbp.Timeslot1},
		Reason: "peer 3132910 is not attached to talkgroup 11 on TS1",
	}}})

	notes := l.RecentCollisions()
	var sum uint64
	for _, n := range notes {
		sum += n.Refusals
	}
	if got := l.Stats().Collisions; sum != got {
		t.Errorf("the record accounts for %d refusals and the counter reads %d", sum, got)
	}
	if len(notes) != 2 {
		t.Fatalf("%d notes, want the key-over and the refusal: %+v", len(notes), notes)
	}
	if notes[0].KeyOver || !notes[1].KeyOver {
		t.Errorf("newest first, the refusal then the key-over: %+v", notes)
	}
}
