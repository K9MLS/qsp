package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
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
	// Listen is the address this side receives on.
	Listen string `json:"listen"`
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
	Reciprocal string `json:"reciprocal"`
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

	inv := peering.Invitation{
		Network:   cfg.DMR.Join.NetworkName,
		Callsign:  linkCallsign(cfg),
		Address:   address,
		NetworkID: firstNetworkID(cfg),
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
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.recordPeering(r, "peering.offered", inv.Callsign, address, audit.OutcomeSuccess)

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

	inv, err := peering.Decode(req.Token)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := inv.Accept(req.Passphrase, time.Now().UTC()); err != nil {
		// Recorded even though nothing was written. An administrator who could
		// not accept a peering is a fact worth having later, and the absence of
		// a record would make it look as though nobody tried.
		s.recordPeering(r, "peering.accepted", inv.Callsign, inv.Address, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(inv.Callsign))
	}
	if name == "" {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": "the link needs a name"})
		return
	}

	cfg := s.opts.Config.Current()
	for _, u := range cfg.DMR.Upstreams {
		if strings.EqualFold(u.Name, name) {
			writeJSON(w, s.log, http.StatusConflict,
				map[string]string{"error": fmt.Sprintf("a link called %q already exists", name)})
			return
		}
	}

	// **The passphrase is written to a file rather than into the
	// configuration.** Configuration is versioned, kept in a database, and
	// shown in a console; a secret in it is a secret in all three.
	path, err := s.writePassphrase(cfg, name, req.Passphrase)
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	slot := req.Timeslot
	if slot != 1 && slot != 2 {
		slot = 2
	}
	tg := req.Talkgroup
	if tg == 0 && len(inv.Import) > 0 {
		tg = inv.Import[0].Talkgroup
	}

	cfg.DMR.Upstreams = append(cfg.DMR.Upstreams, config.Upstream{
		Name:           name,
		Protocol:       config.UpstreamOpenBridge,
		Enabled:        true,
		Address:        inv.Address,
		ListenAddress:  strings.TrimSpace(req.Listen),
		NetworkID:      req.NetworkID,
		PassphraseFile: path,
		Export:         []config.UpstreamTalkgroup{{Talkgroup: tg, Timeslot: slot}},
		Import:         []config.UpstreamTalkgroup{{Talkgroup: tg, Timeslot: slot}},
	})
	// A bridge, because a link with nothing routing to it opens, authenticates
	// and carries nothing — which is the failure this whole page exists after.
	cfg.DMR.Bridges = append(cfg.DMR.Bridges, config.Bridge{
		Name:    name + "-link",
		Enabled: true,
		Endpoints: []config.Endpoint{
			{Talkgroup: tg, Timeslot: slot},
			{Upstream: name, Talkgroup: tg, Timeslot: slot},
		},
	})

	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}
	summary := fmt.Sprintf("peering with %s (%s)", inv.Callsign, inv.Network)

	version, err := s.opts.Config.Save(r.Context(), cfg, author, summary)
	if err != nil {
		s.recordPeering(r, "peering.accepted", inv.Callsign, inv.Address, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.recordPeering(r, "peering.accepted", inv.Callsign, inv.Address, audit.OutcomeSuccess)

	// The reply carries the agreed passphrase's fingerprint, never a new
	// secret: OpenBridge authenticates every datagram against one shared
	// passphrase, and a second would produce a link that works one way while
	// both ends report healthy.
	mine := peering.Invitation{
		Network:   cfg.DMR.Join.NetworkName,
		Callsign:  strings.ToUpper(name),
		Address:   strings.TrimSpace(req.Listen),
		NetworkID: req.NetworkID,
		Export:    []peering.Talkgroup{{Talkgroup: tg, Timeslot: slot}},
		Import:    []peering.Talkgroup{{Talkgroup: tg, Timeslot: slot}},
		Issued:    time.Now().UTC(),
	}
	back, err := peering.Reciprocal(inv, mine)
	reply := ""
	if err == nil {
		reply, _ = peering.Encode(back)
	}

	writeJSON(w, s.log, http.StatusOK, acceptResponse{
		Version:    version.Number,
		Callsign:   inv.Callsign,
		Network:    inv.Network,
		Reciprocal: reply,
	})
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
func (s *Server) recordPeering(r *http.Request, action, callsign, address string, outcome audit.Outcome) {
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
		Action:     audit.Action(action),
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
	for _, u := range cfg.DMR.Upstreams {
		if c := strings.TrimSpace(u.Identity.Callsign); c != "" {
			return c
		}
	}
	return ""
}
