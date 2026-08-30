// Package hotspot generates the configuration a member pastes into their own
// hotspot.
//
// Getting the first hotspot onto this network took two sessions across two
// days, and QSP was never at fault: the obstacles were Enabled=0 in
// /etc/dmrgateway while the dashboard reported the network as enabled, and a
// talkgroup rewrite nobody had written down. The join page already describes
// the settings. Describing them is what costs the evening — a member reading
// prose and typing rules is a member making one of the mistakes above.
//
// Nothing here does I/O or holds state. It takes what the administrator has
// configured plus what the member tells it about their own hotspot, and returns
// text. That keeps it testable against a real configuration file, which is the
// only way to be sure of syntax that gets pasted into fifty hotspots.
package hotspot

import (
	"fmt"
	"sort"
	"strings"
)

// Talkgroup is one talkgroup as the club publishes it.
type Talkgroup struct {
	// Name is what the club calls it.
	Name string
	// Dialled is the number a member enters into the radio when QSP is their
	// only network.
	Dialled uint32
	// Arrives is the number QSP receives, when it differs from Dialled. Zero
	// means no rewrite: the two are the same.
	Arrives uint32
	// Timeslot is 1 or 2.
	Timeslot int
}

// target returns the talkgroup QSP actually receives.
func (t Talkgroup) target() uint32 {
	if t.Arrives != 0 {
		return t.Arrives
	}
	return t.Dialled
}

// Network is everything needed to write a member's network block.
type Network struct {
	// Name is the club's name for this network, as it appears in the block.
	// DMRGateway shows it on the dashboard, so it must not contain spaces.
	Name string
	// Address and Port are where the hotspot connects.
	Address string
	Port    int
	// RadioID is the member's own DMR ID, used for private calls. Zero means
	// unknown, which suppresses the private call rules rather than guessing.
	RadioID uint32
	// Block is which [DMR Network N] slot the member has free. DMRGateway
	// numbers them from 1 and a member may already be using several.
	Block int
	// Prefix is the leading digit that routes a dialled number to this network
	// rather than to another one on the same hotspot.
	//
	// **It is the member's choice, not the club's.** The rewrite happens on
	// their hotspot before anything reaches QSP, so two members may pick
	// different digits with no effect on each other or on the network. What
	// matters is only that it does not collide with the other networks in
	// their own file.
	//
	// Zero means no prefix: the member runs QSP alone and dials talkgroup
	// numbers directly.
	Prefix int
	// Talkgroups are the ones the club publishes.
	Talkgroups []Talkgroup
	// Parrot is the talkgroup carrying the echo service, or zero if the club
	// runs none.
	Parrot uint32
}

// Config is generated configuration together with what the member must check.
type Config struct {
	// Block is the text to paste, without a trailing newline.
	Block string
	// Warnings are things the generator cannot do for the member. They are not
	// errors: the configuration is correct and these still need reading.
	Warnings []string
}

// Errors returned by Render for input it will not guess at.
var (
	ErrNoAddress = fmt.Errorf("hotspot: no address, so a member has nothing to point at")
	ErrBlock     = fmt.Errorf("hotspot: block number must be 1 or greater")
	ErrPrefix    = fmt.Errorf("hotspot: prefix must be a single digit 1 to 9, or zero for none")
)

// Render produces the member's network block.
//
// A prefixed rule set follows the shape every other network on a multi-network
// hotspot already uses: a blanket seven-digit rule so any talkgroup is
// reachable, per-talkgroup shortcuts for the ones the club publishes, private
// call rules so a member can be called back, and source rules so a reply shows
// the number that was dialled rather than the number the network used.
func Render(n Network) (Config, error) {
	if strings.TrimSpace(n.Address) == "" {
		return Config{}, ErrNoAddress
	}
	if n.Block < 1 {
		return Config{}, ErrBlock
	}
	if n.Prefix < 0 || n.Prefix > 9 {
		return Config{}, ErrPrefix
	}

	var b strings.Builder
	var warn []string

	fmt.Fprintf(&b, "[DMR Network %d]\n", n.Block)

	// Enabled first, and deliberately.
	//
	// **The dashboard reporting a network as enabled while this line said
	// otherwise is what cost two days.** Putting it at the top of the block
	// means a member checking their work reads it before anything else.
	b.WriteString("Enabled=1\n")

	fmt.Fprintf(&b, "Name=%s\n", name(n.Name))
	fmt.Fprintf(&b, "Address=%s\n", n.Address)
	fmt.Fprintf(&b, "Port=%d\n", n.Port)
	b.WriteString("Password=\"CHANGE_ME\"\n")
	if n.RadioID != 0 {
		fmt.Fprintf(&b, "Id=%d\n", n.RadioID)
	}
	b.WriteString("Location=0\n")
	b.WriteString("Debug=0\n")

	if n.Prefix == 0 {
		// No other network to share the hotspot with, so every talkgroup
		// passes unchanged and there is nothing to rewrite. Rules here would
		// be noise a member has to maintain for no benefit.
		for _, s := range []int{1, 2} {
			fmt.Fprintf(&b, "PassAllTG%d=%d\n", s-1, s)
			fmt.Fprintf(&b, "PassAllPC%d=%d\n", s-1, s)
		}
		warn = append(warn, passwordWarning, contentWarning)
		return Config{Block: b.String(), Warnings: warn}, nil
	}

	// The blanket rule: prefix followed by six digits reaches any talkgroup.
	// It is what makes a talkgroup the club adds later work without the member
	// editing anything.
	base := uint32(n.Prefix) * 1000000
	var tg, pc, src, typ int
	for _, slot := range []int{1, 2} {
		fmt.Fprintf(&b, "TGRewrite%d=%d,%d,%d,1,999999\n", tg, slot, base+1, slot)
		tg++
	}

	// Shortcuts for the talkgroups the club publishes, so a member dials a
	// short number for the ones they use daily. Sorted, because a generator
	// whose output reorders between runs makes a diff useless.
	for _, t := range sorted(n.Talkgroups) {
		if t.Timeslot != 1 && t.Timeslot != 2 {
			continue
		}
		fmt.Fprintf(&b, "TGRewrite%d=%d,%d,%d,%d,1\n",
			tg, t.Timeslot, base+t.Dialled, t.Timeslot, t.target())
		tg++
	}

	// Private calls in both directions, so a member can be called and can call
	// back. Without Id there is no member to address, and a rule naming the
	// wrong radio silently sends texts nowhere.
	if n.RadioID != 0 {
		for _, slot := range []int{1, 2} {
			fmt.Fprintf(&b, "PCRewrite%d=%d,%d,%d,1,999999\n", pc, slot, base+1, slot)
			pc++
		}
	} else {
		warn = append(warn, "This block carries no private call rules, because QSP has not "+
			"heard your radio yet and will not guess its ID. Key up once and reload this "+
			"page, or fill in your DMR ID above.")
	}

	// The parrot is reached as a group call on QSP, and a member coming from
	// another network has it programmed as a private call. TypeRewrite is
	// where that difference is absorbed, on the hotspot, which is why QSP
	// never has to rewrite a Link Control to support it. See ADR-0028.
	if n.Parrot != 0 {
		for _, slot := range []int{1, 2} {
			fmt.Fprintf(&b, "TypeRewrite%d=%d,%d,%d,%d\n",
				typ, slot, base+n.Parrot, slot, n.Parrot)
			typ++
		}
	}

	// The return path. Without it a reply arrives showing the number this
	// network uses rather than the number the member dialled, and the two
	// disagreeing is how somebody concludes they reached the wrong place.
	for _, slot := range []int{1, 2} {
		fmt.Fprintf(&b, "SrcRewrite%d=%d,1,%d,%d,999999\n", src, slot, slot, base+1)
		src++
	}

	warn = append(warn,
		passwordWarning,
		fmt.Sprintf("Check that no other network in your file already uses %d as its "+
			"leading digit. If one does, pick another digit here and generate this "+
			"again — a collision sends your traffic to the wrong network and every "+
			"log looks healthy.", n.Prefix),
		fmt.Sprintf("Check that [DMR Network %d] is not already in use. DMRGateway "+
			"numbers these from 1 and does not warn about a duplicate.", n.Block),
		contentWarning,
	)

	return Config{Block: b.String(), Warnings: warn}, nil
}

const (
	passwordWarning = "Replace CHANGE_ME with the password you were sent. It is not shown " +
		"on this page, and it is the one thing here that must not travel by email."

	// **Learned the hard way.** A line-numbered edit to this file affects every
	// network block that happens to match, and taking the wrong one out takes
	// the hotspot off every network at once.
	contentWarning = "When you edit /etc/dmrgateway, find lines by searching for them, " +
		"never by line number. The same setting appears in every network block, and an " +
		"edit by position removes the wrong one."
)

// name makes a value safe for the Name field, which DMRGateway shows on a
// dashboard and which cannot carry spaces.
func name(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "QSP"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// sorted orders talkgroups by timeslot then dialled number, so the same
// configuration always renders identically.
func sorted(in []Talkgroup) []Talkgroup {
	out := make([]Talkgroup, 0, len(in))
	for _, t := range in {
		if t.Dialled != 0 {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Timeslot != out[j].Timeslot {
			return out[i].Timeslot < out[j].Timeslot
		}
		return out[i].Dialled < out[j].Dialled
	})
	return out
}
