package hotspot

import (
	"strings"
	"testing"
)

// The shape these tests assert comes from a working /etc/dmrgateway carrying
// five networks — BrandMeister, DMR+ IPSC2, HBLink and SystemX among them —
// rather than from documentation. Three of those networks implement the same
// prefix scheme with different digits, which is where the rule set below is
// taken from.

func club() Network {
	return Network{
		Name:    "QSP",
		Address: "qsp.hopto.me",
		Port:    62031,
		RadioID: 3132910,
		Block:   6,
		Prefix:  7,
		Parrot:  9990,
		Talkgroups: []Talkgroup{
			{Name: "Club", Dialled: 9, Timeslot: 2},
			{Name: "Chat", Dialled: 11, Timeslot: 2},
			{Name: "Wide", Dialled: 3148, Timeslot: 1},
		},
	}
}

func lines(t *testing.T, n Network) (map[string]bool, Config) {
	t.Helper()
	cfg, err := Render(n)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	set := map[string]bool{}
	for _, l := range strings.Split(cfg.Block, "\n") {
		if l != "" {
			set[l] = true
		}
	}
	return set, cfg
}

// TestEnabledComesFirst.
//
// **The dashboard reporting a network as enabled while the file said Enabled=0
// is what cost this project two days.** A member checking their work reads from
// the top, so the line that lied to them last time is the first one they see.
func TestEnabledComesFirst(t *testing.T) {
	cfg, err := Render(club())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := strings.Split(cfg.Block, "\n")
	if len(got) < 2 {
		t.Fatalf("block is %d lines", len(got))
	}
	if got[0] != "[DMR Network 6]" {
		t.Errorf("first line is %q, not the section header", got[0])
	}
	if got[1] != "Enabled=1" {
		t.Errorf("second line is %q; Enabled must be the first setting a member reads", got[1])
	}
}

// TestAnyTalkgroupIsReachable. The club will add a talkgroup after a member has
// pasted this, and a rule set naming only today's talkgroups would leave them
// unable to reach it without editing a file again.
func TestAnyTalkgroupIsReachable(t *testing.T) {
	set, _ := lines(t, club())
	for _, want := range []string{
		"TGRewrite0=1,7000001,1,1,999999",
		"TGRewrite1=2,7000001,2,1,999999",
	} {
		if !set[want] {
			t.Errorf("no blanket rule %q; a talkgroup added later would be unreachable", want)
		}
	}
}

// TestNoTalkgroupIsEverRenumbered.
//
// **A talkgroup number is the same on both sides of a hotspot.** An earlier
// version emitted a per-talkgroup rule for every published number, so a club
// adding one made every member edit a file again — and any of those rules could
// map a number to a different number. That is how a network ends up carrying a
// rewrite nobody remembers writing, and the symptom is a member transmitting
// into silence with every log healthy.
//
// The blanket rule reaches every talkgroup and preserves the number: dial the
// prefix followed by 11 and 11 arrives.
func TestNoTalkgroupIsEverRenumbered(t *testing.T) {
	cfg, err := Render(club())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, line := range strings.Split(cfg.Block, "\n") {
		if !strings.HasPrefix(line, "TGRewrite") {
			continue
		}
		parts := strings.Split(strings.SplitN(line, "=", 2)[1], ",")
		if len(parts) != 5 {
			t.Errorf("%q is not a rule this can read", line)
			continue
		}
		if parts[1] != "7000001" || parts[3] != "1" || parts[4] != "999999" {
			t.Errorf("%q renumbers a talkgroup; only the prefix may be stripped", line)
		}
	}
	if n := strings.Count(cfg.Block, "TGRewrite"); n != 2 {
		t.Errorf("the block carries %d talkgroup rules; a club adding a talkgroup would "+
			"mean every member editing a file again", n)
	}

	again, err := Render(club())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if again.Block != cfg.Block {
		t.Error("two renders of one configuration differ; a diff cannot be trusted")
	}
}

// TestNoRadioIDMeansNoPrivateCallRules.
//
// The suffix convention is a convention and not a rule of the protocol, which
// the access work already established. A rule naming a guessed radio sends a
// member's texts somewhere they will never look for them, and silence is the
// better failure.
func TestNoRadioIDMeansNoPrivateCallRules(t *testing.T) {
	n := club()
	n.RadioID = 0

	cfg, err := Render(n)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(cfg.Block, "PCRewrite") {
		t.Error("private call rules were written without knowing the member's radio ID")
	}
	if strings.Contains(cfg.Block, "Id=") {
		t.Error("an Id was written without knowing the member's radio ID")
	}
	if !warned(cfg, "has not heard your radio") {
		t.Error("the missing private call rules are not explained to the member")
	}
}

// TestTheParrotIsConvertedOnTheHotspot.
//
// ADR-0028 records that QSP replays bytes it never understood and so cannot
// turn a group call into a private one. TypeRewrite does it on the hotspot,
// before anything reaches QSP — which is how a member keeps the private-call
// parrot they already have programmed.
func TestTheParrotIsConvertedOnTheHotspot(t *testing.T) {
	set, _ := lines(t, club())
	for _, want := range []string{
		"TypeRewrite0=1,7009990,1,9990",
		"TypeRewrite1=2,7009990,2,9990",
	} {
		if !set[want] {
			t.Errorf("missing %q, so a member's parrot arrives as the wrong call type", want)
		}
	}

	n := club()
	n.Parrot = 0
	cfg, err := Render(n)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(cfg.Block, "TypeRewrite") {
		t.Error("a club with no parrot got parrot rules")
	}
}

// TestARelpyShowsTheNumberThatWasDialled. Without the source rules, an answer
// arrives displaying the club's number rather than the one the member entered,
// and two numbers for one conversation is how somebody concludes they reached
// the wrong network.
func TestARelpyShowsTheNumberThatWasDialled(t *testing.T) {
	set, _ := lines(t, club())
	for _, want := range []string{
		"SrcRewrite0=1,1,1,7000001,999999",
		"SrcRewrite1=2,1,2,7000001,999999",
	} {
		if !set[want] {
			t.Errorf("missing return rule %q", want)
		}
	}
}

// TestOneNetworkNeedsNoRewritingAtAll. Most members run QSP alone. Rules for
// them would be maintenance with no purpose, and every rule is a thing that can
// be wrong.
func TestOneNetworkNeedsNoRewritingAtAll(t *testing.T) {
	n := club()
	n.Prefix = 0

	cfg, err := Render(n)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, banned := range []string{"TGRewrite", "PCRewrite", "SrcRewrite", "TypeRewrite"} {
		if strings.Contains(cfg.Block, banned) {
			t.Errorf("a single-network hotspot was given %s rules", banned)
		}
	}
	for _, want := range []string{"PassAllTG0=1", "PassAllTG1=2", "PassAllPC0=1", "PassAllPC1=2"} {
		if !strings.Contains(cfg.Block, want) {
			t.Errorf("missing %q, so traffic would not pass", want)
		}
	}
}

// TestTheMemberIsToldWhatThisCannotCheck. The prefix and the block number are
// facts about a file only the member can see. Generating them silently is how a
// collision reaches the air.
func TestTheMemberIsToldWhatThisCannotCheck(t *testing.T) {
	_, cfg := lines(t, club())
	for _, want := range []string{
		"leading digit",
		"already in use",
		"never by line number",
		"CHANGE_ME",
	} {
		if !warned(cfg, want) {
			t.Errorf("no warning mentions %q", want)
		}
	}
}

// TestThePasswordIsNeverGenerated. Everything on the join page is safe to show
// anybody who can reach it. The password is the one thing that is not.
func TestThePasswordIsNeverGenerated(t *testing.T) {
	for _, prefix := range []int{0, 7} {
		n := club()
		n.Prefix = prefix
		cfg, err := Render(n)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if !strings.Contains(cfg.Block, `Password="CHANGE_ME"`) {
			t.Errorf("prefix %d: the password placeholder is missing", prefix)
		}
	}
}

// TestRenderRefusesWhatItWillNotGuess.
func TestRenderRefusesWhatItWillNotGuess(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Network)
		want error
	}{
		{"no address", func(n *Network) { n.Address = "  " }, ErrNoAddress},
		{"no block", func(n *Network) { n.Block = 0 }, ErrBlock},
		{"two digit prefix", func(n *Network) { n.Prefix = 42 }, ErrPrefix},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := club()
			c.edit(&n)
			if _, err := Render(n); err != c.want {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

// TestTheNameCannotBreakTheFile. DMRGateway shows Name on a dashboard and the
// field does not survive a space.
func TestTheNameCannotBreakTheFile(t *testing.T) {
	n := club()
	n.Name = "Benbrook Amateur Radio"

	cfg, err := Render(n)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(cfg.Block, "Name=Benbrook_Amateur_Radio\n") {
		t.Error("a name with spaces was written into the block unchanged")
	}

	n.Name = ""
	cfg, err = Render(n)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(cfg.Block, "Name=QSP\n") {
		t.Error("an unnamed network produced no name at all")
	}
}

func warned(c Config, substr string) bool {
	for _, w := range c.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
