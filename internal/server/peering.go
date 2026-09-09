package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/bindcheck"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/peering"
)

// Peering is how a link stops being a configuration edit.
//
// **Two instances linked and neither administrator was asked to confirm
// anything.** The consent was real — OpenBridge has no connection, so a link
// exists only because both sides hold a passphrase agreed out of band — and it
// was invisible: nothing showed the far end, nothing recorded who agreed, and a
// test passphrase was accepted without complaint. ADR-0032 decided the
// out-of-band exchange becomes an artefact rather than a conversation, and this
// is where a person does it.
//
// Nothing here changes a byte on the wire. It writes configuration and a
// passphrase file, and records who did.

// offerRequest asks this instance to propose a peering.
type offerRequest struct {
	// Talkgroup and Timeslot are what this side proposes to exchange.
	Talkgroup uint32 `json:"talkgroup"`
	Timeslot  int    `json:"timeslot"`
	// Address is where the far end should send, as host:port. Empty asks the
	// instance to build one from its own join address, which is right often
	// enough to be worth offering and wrong behind a NAT.
	Address string `json:"address"`
	// NetworkID is what this instance will announce on the link.
	//
	// **Without this the first peering an operator ever attempted failed.** It
	// was taken from an existing link, and an instance with no links has none,
	// so the invitation was refused for carrying no network ID — on precisely
	// the instance that has never peered with anything, which is every
	// instance the first time.
	NetworkID uint32 `json:"network_id"`
	// Callsign is who is asking, and it is required.
	//
	// **The same defect, from the other end, and it survived the first fix.**
	// linkCallsign was taught to read dmr.identity before the links — but
	// config.Default() leaves identity empty and nothing in QSP has ever asked
	// an operator to fill it in, so the callsign was still absent on a fresh
	// instance. The invitation was refused naming a field the page did not
	// have, and the error then told the operator to check the network ID and
	// the address, which were both already correct.
	//
	// Supplied here, saved as the instance's identity, and filled in from
	// there next time. See handleOfferPeering.
	Callsign string `json:"callsign"`
}

// offerResponse carries the two halves that travel by different routes.
type offerResponse struct {
	// Token is the invitation, safe to send by email.
	Token string `json:"token"`
	// Passphrase is shown exactly once.
	//
	// **It is not in the token, deliberately.** The token goes by email, which
	// is what the join page already refuses to send a peer password over.
	// Splitting them means a forwarded thread is not a credential.
	Passphrase string `json:"passphrase"`
	// Fingerprint lets the other administrator confirm they typed the right
	// thing before either of them waits on a silent link.
	Fingerprint string `json:"fingerprint"`
}

// acceptRequest is an invitation and the passphrase that came separately.
type acceptRequest struct {
	Token      string `json:"token"`
	Passphrase string `json:"passphrase"`
	// Name is what this instance will call the link locally.
	Name string `json:"name"`
	// Talkgroup and Timeslot are the local numbers to carry over it.
	Talkgroup uint32 `json:"talkgroup"`
	Timeslot  int    `json:"timeslot"`
	// Listen is the local address this side binds to receive on.
	//
	// **It must be an address this host holds**, so 0.0.0.0 or a LAN address,
	// never a public name.
	Listen string `json:"listen"`
	// Address is where the far end should send: a public name or address and a
	// UDP port that reaches this instance from the internet.
	//
	// # Why this is a second box
	//
	// Listen used to serve both roles, and they are exact opposites. Every
	// value an operator could type was wrong in one of three ways:
	//
	//   - 0.0.0.0:62045, which is what the page suggested, binds correctly and
	//     is refused by Invitation.Validate as somewhere to send to. The error
	//     was discarded, so the reciprocal came back empty with nothing said.
	//   - A public name produces a valid reciprocal and an address this host
	//     cannot bind. QSP then refuses to start — correctly — and systemd
	//     crash-loops to its start limit. **This is what happened.**
	//   - A LAN address binds, validates, and tells a far end across the
	//     internet to send somewhere it cannot reach: a link that reports
	//     itself configured and carries silence.
	//
	// The offer form has had these as two fields all along. The accept form
	// had one.
	Address string `json:"address"`
	// NetworkID is what this instance announces on the link.
	NetworkID uint32 `json:"network_id"`
	// Confirm must be true. A peering is agreed, and a request that could be
	// made by accident is not an agreement.
	Confirm bool `json:"confirm"`
}

// acceptResponse reports what was written.
type acceptResponse struct {
	// Version is the configuration version the link was written into.
	Version int64 `json:"version"`
	// Callsign and Network name the far end, echoed so the console can show
	// who was just agreed to rather than only that something was.
	Callsign string `json:"callsign"`
	Network  string `json:"network"`
	// Reciprocal is the invitation to send back, carrying the agreed
	// passphrase's fingerprint rather than a new secret.
	//
	// **Empty when there is nobody to send one to.** Accepting the reply to an
	// offer this instance made is the end of the exchange, and a reciprocal
	// there produced an endless one.
	Reciprocal string `json:"reciprocal"`
	// Complete reports that both halves are now configured and there is
	// nothing further to send.
	Complete bool `json:"complete"`
	// NeedsRestart names the settings that were written and cannot take
	// effect until QSP restarts.
	//
	// **A peering opens no socket.** Upstreams are built once at startup and
	// a configuration apply does not touch them, so an accepted link carries
	// nothing in either direction until a restart. `config.NeedsRestart` has
	// named `dmr.upstreams` all along and this page never asked it, so the
	// console reported a peering agreed and left an operator waiting on a
	// link that did not exist yet.
	NeedsRestart []string `json:"needs_restart,omitempty"`
}

// handleOfferPeering generates an invitation and the passphrase behind it.
func (s *Server) handleOfferPeering(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{"error": "this instance cannot be configured from here"})
		return
	}

	var req offerRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}
	cfg := s.opts.Config.Current()

	address := strings.TrimSpace(req.Address)
	if address == "" {
		// A guess, and said to be one on the page. An instance behind NAT
		// announces an address the far end cannot reach, and the alternative
		// is an empty box an administrator has to fill from knowledge the
		// instance does not have either.
		address = defaultLinkAddress(cfg)
	}

	passphrase, err := peering.NewPassphrase()
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{"error": "cannot generate a passphrase"})
		return
	}

	networkID := req.NetworkID
	if networkID == 0 {
		networkID = firstNetworkID(cfg)
	}

	// The typed callsign wins, and is remembered. An instance that has one
	// configured does not need it typed again, which is why the field arrives
	// pre-filled.
	callsign := strings.ToUpper(strings.TrimSpace(req.Callsign))
	if callsign == "" {
		callsign = linkCallsign(cfg)
	}
	if callsign != "" && !strings.EqualFold(callsign, cfg.DMR.Identity.Callsign) {
		// **Not fatal if it cannot be written.** An invitation the operator can
		// send is worth more than a saved preference, and the identity can be
		// set in Administration afterwards. Failing the offer because the
		// convenience failed would be the defect this whole change removes.
		saved := cfg
		saved.DMR.Identity.Callsign = callsign
		author := "unknown"
		if sess, ok := SessionFrom(r.Context()); ok {
			author = sess.Username
		}
		if _, err := s.opts.Config.Save(r.Context(), saved,
			author, "instance callsign, set while offering a peering"); err != nil {
			s.log.Warn("could not save the instance callsign", "error", err)
		} else {
			cfg = saved
		}
	}

	// Held so that the reciprocal can be accepted without asking for a secret
	// this instance generated itself. See handleAcceptPeering.
	s.offered.put(peering.FingerprintOf(passphrase), passphrase)

	inv := peering.Invitation{
		Network:   cfg.DMR.Join.NetworkName,
		Callsign:  callsign,
		Address:   address,
		NetworkID: networkID,
		Export: []peering.Talkgroup{
			{Talkgroup: req.Talkgroup, Timeslot: req.Timeslot},
		},
		Import: []peering.Talkgroup{
			{Talkgroup: req.Talkgroup, Timeslot: req.Timeslot},
		},
		Fingerprint: peering.FingerprintOf(passphrase),
		Issued:      time.Now().UTC(),
	}

	token, err := peering.Encode(inv)
	if err != nil {
		// An invitation is refused for a missing field on an instance that has
		// never peered, which is when an operator is least able to guess which
		// field it means. Say what to fill in rather than what is absent.
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": err.Error() +
				" — fill in the network ID and the address the far end should send to",
		})
		return
	}

	s.recordPeering(r, audit.ActionPeeringOffered, inv.Callsign, address, audit.OutcomeSuccess)

	writeJSON(w, s.log, http.StatusOK, offerResponse{
		Token:       token,
		Passphrase:  passphrase,
		Fingerprint: inv.Fingerprint,
	})
}

// handleAcceptPeering writes a link an administrator has agreed to.
func (s *Server) handleAcceptPeering(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{"error": "this instance cannot be configured from here"})
		return
	}

	var req acceptRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}
	if !req.Confirm {
		writeJSON(w, s.log, http.StatusBadRequest,
			map[string]string{"error": "a peering has to be confirmed; nothing was written"})
		return
	}

	// **One box, and the token says which form it is.** An operator holding a
	// token cannot be asked which kind it is without being asked to read
	// base64, and the two write entirely different configuration: an
	// OpenBridge peering with a bridge and a timeslot, or a single upstream
	// block. See acceptLink.
	switch kind, err := peering.KindOf(req.Token); {
	case err != nil:
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case kind == peering.KindLink:
		s.acceptLink(w, r, req)
		return
	}

	inv, err := peering.Decode(req.Token)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// **The side that offered already has the passphrase; it generated it.**
	//
	// A reciprocal carries the agreed secret's fingerprint rather than a new
	// secret — Reciprocal says so — so when the offering administrator pastes
	// one back there is nothing for them to type, and the form demanded it
	// anyway. The peering stopped there with "a passphrase must be at least 24
	// characters" beneath an empty box nobody could fill.
	//
	// Looked up by fingerprint from the offers this instance has outstanding.
	// An offer that this instance did not make has none, and the operator is
	// asked for the passphrase as before.
	//
	// **It also tells us which half of the exchange this is**, which is what
	// stops the loop below: a passphrase we are holding means this invitation
	// is the reply to an offer we made, and the peering ends here.
	// **The store is consulted whether or not a passphrase was typed.** The
	// condition used to be `passphrase == ""`, so an operator who filled in the
	// box — holding a passphrase they generated themselves, beside a box asking
	// for one — skipped the lookup entirely, and the exchange built another
	// reciprocal. Forever. Termination cannot depend on somebody leaving a
	// field blank.
	//
	// Peeked rather than taken. It is forgotten after the peering is written,
	// so a refusal below leaves the operator able to retry.
	heldPassphrase, weOfferedThis := "", false
	if inv.Fingerprint != "" {
		heldPassphrase, weOfferedThis = s.offered.peek(inv.Fingerprint)
	}
	passphrase := req.Passphrase
	if strings.TrimSpace(passphrase) == "" && weOfferedThis {
		passphrase = heldPassphrase
	}

	// **inv.Reply is the durable half of this and weOfferedThis is the
	// fallback.** A reciprocal says so in the token, so a restart between the
	// two halves no longer loses the only evidence that a peering is finished.
	// The memory still answers for a token written by a QSP from before the
	// field existed.
	closingOurOffer := inv.Reply || weOfferedThis

	if err := inv.Accept(passphrase, time.Now().UTC()); err != nil {
		// Recorded even though nothing was written. An administrator who could
		// not accept a peering is a fact worth having later, and the absence of
		// a record would make it look as though nobody tried.
		s.recordPeering(r, audit.ActionPeeringAccepted, inv.Callsign, inv.Address, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(inv.Callsign))
	}
	// **Checked here as well as in Validate, because the passphrase file is
	// written before the configuration is saved.** The name becomes that
	// file's path, so waiting for Save to refuse it would mean refusing after
	// the file had already been written somewhere it should not be.
	if err := config.ValidUpstreamName(name); err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	cfg := s.opts.Config.Current()
	// Kept so the restart notice is derived rather than asserted. A hardcoded
	// "restart to apply" would be a second place to keep true, and
	// config.NeedsRestart is the first.
	before := cfg
	for _, u := range cfg.DMR.Upstreams {
		if strings.EqualFold(u.Name, name) {
			writeJSON(w, s.log, http.StatusConflict,
				map[string]string{"error": fmt.Sprintf("a link called %q already exists", name)})
			return
		}
	}

	slot := req.Timeslot
	if slot != 1 && slot != 2 {
		slot = 2
	}
	tg := req.Talkgroup
	if tg == 0 && len(inv.Import) > 0 {
		tg = inv.Import[0].Talkgroup
	}

	// **Everything that can refuse this peering refuses it here**, above the
	// first line that writes anything.
	//
	// The passphrase file was written before any address was looked at, so a
	// refusal below it left a .pass file behind for a link that was never
	// created. A refusal leaves nothing behind.
	listen := strings.TrimSpace(req.Listen)
	if err := bindcheck.Address("udp", listen); err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": listenProblem(listen, err),
		})
		return
	}

	// The reciprocal is built now rather than after the configuration is
	// saved, for the same reason. It used to be built last and its error
	// discarded; reporting that error from where it stood would have put a
	// message on screen and a link on disk.
	reply, err := s.reciprocalFor(cfg, inv, req, tg, slot, closingOurOffer)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// **The passphrase is written to a file rather than into the
	// configuration.** Configuration is versioned, kept in a database, and
	// shown in a console; a secret in it is a secret in all three.
	path, err := s.writePassphrase(cfg, name, passphrase)
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	cfg.DMR.Upstreams = append(cfg.DMR.Upstreams, config.Upstream{
		Name:           name,
		Protocol:       config.UpstreamOpenBridge,
		Enabled:        true,
		Address:        inv.Address,
		ListenAddress:  listen,
		NetworkID:      req.NetworkID,
		PassphraseFile: path,
		// The export and import lists were written here until 0308 and read by
		// nothing. The bridge below is what actually carries the traffic, and
		// always was.
	})
	// A bridge, because a link with nothing routing to it opens, authenticates
	// and carries nothing — which is the failure this whole page exists after.
	//
	// **The endpoint naming the link is timeslot 1, whatever the form asked.**
	// OpenBridge passes all traffic on TS1 — openbridge.Encode forces it — so
	// every frame crossing the link arrives as TS1 at the far end and leaves as
	// TS1 from here. An endpoint on TS2 therefore matches nothing in either
	// direction: it is not a preference the operator can hold, it is a bridge
	// that cannot carry.
	//
	// This form defaulted to 2, and both ends of a peering were configured,
	// both links reported healthy, and no audio crossed in either direction for
	// a day. The counters read Sent 50 / Received 0 on one side and Received 28
	// / Sent 0 on the other, which looks exactly like a network fault.
	//
	// The local endpoint keeps the operator's slot: that one is a real choice
	// about this network's own peers. config.Validate refuses any other value
	// on the link endpoint, so a hand-edited document cannot recreate it.
	cfg.DMR.Bridges = append(cfg.DMR.Bridges, config.Bridge{
		Name:    name + "-link",
		Enabled: true,
		Endpoints: []config.Endpoint{
			{Talkgroup: tg, Timeslot: slot},
			{Upstream: name, Talkgroup: tg, Timeslot: config.OpenBridgeTimeslot},
		},
	})

	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}
	summary := fmt.Sprintf("peering with %s (%s)", inv.Callsign, inv.Network)

	version, err := s.opts.Config.Save(r.Context(), cfg, author, summary)
	if err != nil {
		s.recordPeering(r, audit.ActionPeeringAccepted, inv.Callsign, inv.Address, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.recordPeering(r, audit.ActionPeeringAccepted, inv.Callsign, inv.Address, audit.OutcomeSuccess)

	// Written, so the secret can stop living in memory as well as in its file.
	if weOfferedThis {
		s.offered.forget(inv.Fingerprint)
	}

	writeJSON(w, s.log, http.StatusOK, acceptResponse{
		Version:      version.Number,
		Callsign:     inv.Callsign,
		Network:      inv.Network,
		Reciprocal:   reply,
		Complete:     closingOurOffer,
		NeedsRestart: config.NeedsRestart(before, cfg),
	})
}

// listenProblem explains a listen address this host cannot bind, in the terms
// of the box it was typed into.
//
// The operating system's own sentence is kept, because "cannot assign requested
// address" is the precise fact and an operator who searches for it finds the
// same thing QSP's journal said. What is added is which of the two boxes this
// is and what belongs in it.
func listenProblem(listen string, err error) string {
	switch {
	case errors.Is(err, bindcheck.ErrNoAddress):
		return `"We listen on" is where this server binds and cannot be empty — ` +
			`0.0.0.0:62045 means every interface on this machine`
	case errors.Is(err, bindcheck.ErrInUse):
		return fmt.Sprintf("something is already listening on %s — "+
			"another link may already use that port", listen)
	default:
		return fmt.Sprintf("this server cannot listen on %s: %v — "+
			`"We listen on" is an address this machine holds, so 0.0.0.0:62045 `+
			`or a local address. The public name goes in "They send to us at"`,
			listen, err)
	}
}

// reciprocalFor builds the invitation to send back, or reports why it cannot.
//
// **A reciprocal only when there is somebody to send one to**, and closing our
// own offer is the half of the exchange that is not. This used to build one
// unconditionally, so accepting a reply produced another reply, which looked
// like another thing to send back, forever. `closingOurOffer` is the evidence:
// this instance was holding the passphrase, which only happens for a reply to
// an offer it made itself.
//
// **Both errors below were discarded.** `peering.Reciprocal` validates, and it
// refused 0.0.0.0 — the value the accept form suggested — while the caller
// tested only for `err == nil` and left `reply` empty. The console then showed
// an empty box under "send this back" and said nothing, which is how an
// operator learns that a validation exists only by never seeing it fire.
func (s *Server) reciprocalFor(cfg config.Config, inv peering.Invitation,
	req acceptRequest, tg uint32, slot int, closingOurOffer bool) (string, error) {
	if closingOurOffer {
		return "", nil
	}

	address := strings.TrimSpace(req.Address)
	if address == "" {
		return "", errors.New(`"They send to us at" is where the other network sends: ` +
			`a public name or address and a UDP port they can reach, like qsp.example.com:62045`)
	}

	// The reply carries the agreed passphrase's fingerprint, never a new
	// secret: OpenBridge authenticates every datagram against one shared
	// passphrase, and a second would produce a link that works one way while
	// both ends report healthy.
	mine := peering.Invitation{
		Network: cfg.DMR.Join.NetworkName,
		// **The instance's callsign, not the link's name.** This sent
		// strings.ToUpper(name), so a link an operator called "Test Server"
		// announced itself to the far end as TEST SERVER. The callsign is what
		// the other administrator is shown to decide whether they know who is
		// asking.
		Callsign:  linkCallsign(cfg),
		Address:   address,
		NetworkID: req.NetworkID,
		Export:    []peering.Talkgroup{{Talkgroup: tg, Timeslot: slot}},
		Import:    []peering.Talkgroup{{Talkgroup: tg, Timeslot: slot}},
		Issued:    time.Now().UTC(),
	}

	back, err := peering.Reciprocal(inv, mine)
	if err != nil {
		return "", err
	}
	token, err := peering.Encode(back)
	if err != nil {
		return "", err
	}
	return token, nil
}

// writePassphrase stores the agreed secret beside the peer password file, at
// mode 0600.
func (s *Server) writePassphrase(cfg config.Config, name, passphrase string) (string, error) {
	dir := filepath.Dir(cfg.DMR.PasswordFile)
	if dir == "" || dir == "." {
		return "", fmt.Errorf("no directory to write a passphrase into; set dmr.password_file")
	}
	path := filepath.Join(dir, name+".pass")
	if err := os.WriteFile(path, []byte(passphrase), 0o600); err != nil {
		return "", fmt.Errorf("cannot write the passphrase file: %w", err)
	}
	return path, nil
}

// recordPeering writes the audit event ADR-0032 requires.
func (s *Server) recordPeering(r *http.Request, action audit.Action, callsign, address string, outcome audit.Outcome) {
	if s.opts.Audit == nil {
		return
	}
	actor := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		actor = sess.Username
	}
	if err := s.opts.Audit.Record(r.Context(), audit.Event{
		OccurredAt: time.Now().UTC(),
		Actor:      actor,
		Action:     action,
		Outcome:    outcome,
		SourceIP:   clientIP(r, s.opts.BehindProxy),
		Detail:     map[string]string{"callsign": callsign, "address": address},
	}); err != nil {
		s.log.Warn("cannot record a peering in the audit trail", "error", err)
	}
}

// defaultLinkAddress guesses where a far end should send.
func defaultLinkAddress(cfg config.Config) string {
	host := strings.TrimSpace(cfg.DMR.Join.Address)
	if host == "" {
		return ""
	}
	return host + ":62045"
}

// firstNetworkID reuses an existing link's announced ID, or falls back to zero
// so the operator is asked rather than given a number nobody chose.
func firstNetworkID(cfg config.Config) uint32 {
	for _, u := range cfg.DMR.Upstreams {
		if u.NetworkID != 0 {
			return u.NetworkID
		}
	}
	return 0
}

// decodeJSON reads a request body, answering the caller on failure.
func decodeJSON(w http.ResponseWriter, log *slog.Logger, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		writeJSON(w, log, http.StatusBadRequest,
			map[string]string{"error": "cannot read the request: " + err.Error()})
		return false
	}
	return true
}

// linkCallsign is who this instance says it is on a link.
//
// An identity per link is what the configuration carries today, so the first
// one that names a callsign is used and an instance with no links has none. It
// is a claim either way — checkable against RadioID.net by a person, and not by
// QSP, because an operator agreeing to peer has already decided who they are
// dealing with.
func linkCallsign(cfg config.Config) string {
	// **The instance first.** This used to read only the links, so the first
	// peering an operator ever attempted had no callsign to offer — on
	// precisely the instance that has never peered with anything, which is
	// every instance the first time.
	if c := strings.TrimSpace(cfg.DMR.Identity.Callsign); c != "" {
		return c
	}
	for _, u := range cfg.DMR.Upstreams {
		if u.Identity == nil {
			continue
		}
		if c := strings.TrimSpace(u.Identity.Callsign); c != "" {
			return c
		}
	}
	return ""
}
