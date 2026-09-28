package server

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
)

// Sending a text from the console.
//
// ADR-0067 phase 2's trigger: an administrator composes a group text and QSP
// sends it to one hotspot, and the instrument is a radio's display. It is an
// authenticated action rather than something a radio can reach, so it adds no
// surface to the network, and it is the path ADR-0068's administrator
// bulletins will need anyway.

// TextSender composes a text and sends it to one peer. *peers.Listener
// satisfies it.
type TextSender interface {
	SendText(peer hbp.RepeaterID, slot hbp.Timeslot, m tms.Message) error
}

// adminTexts tells the administration page whether it can offer the form.
type adminTexts struct {
	// Available is false when the DMR listener is not running, which is the
	// only thing a text can leave by.
	Available bool `json:"available"`
	// MaxCharacters is the encoder's limit, so the page can say it before
	// the server refuses.
	MaxCharacters int `json:"max_characters"`
}

// sendTextRequest is what the form posts.
type sendTextRequest struct {
	Peer      uint32 `json:"peer"`
	Timeslot  int    `json:"timeslot"`
	Talkgroup uint32 `json:"talkgroup"`
	From      uint32 `json:"from"`
	Text      string `json:"text"`
}

// validate refuses a request that could not become a message, in the words
// the page shows.
func (q sendTextRequest) validate() error {
	switch {
	case q.Peer == 0:
		return errors.New("which hotspot? Its ID is on the Network page")
	case q.Timeslot != 1 && q.Timeslot != 2:
		return errors.New("the timeslot is 1 or 2")
	case q.Talkgroup == 0 || q.Talkgroup > 0xffffff:
		return errors.New("the talkgroup is a number from 1 to 16777215")
	case q.From == 0 || q.From > 0xffffff:
		return errors.New("the sender is a DMR ID from 1 to 16777215")
	case strings.TrimSpace(q.Text) == "":
		return errors.New("there is no text to send")
	}
	for _, r := range q.Text {
		if unicode.IsControl(r) {
			return errors.New("the text holds a control character; a radio has no way to show one")
		}
	}
	if n := len(utf16.Encode([]rune(q.Text))); n > tms.MaxText {
		return fmt.Errorf("the text is %d characters and one message holds %d", n, tms.MaxText)
	}
	return nil
}

// handleSendText composes a group text and sends it to one hotspot.
//
// **Audited whatever the outcome**, like the dongle: a message that appears
// on radios is exactly the kind of thing somebody asks about afterwards,
// including an attempt that was refused.
func (s *Server) handleSendText(w http.ResponseWriter, r *http.Request) {
	if s.opts.Texts == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "the DMR listener is not running on this instance, so there is nothing to send a text through"})
		return
	}
	var req sendTextRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	m := tms.Message{
		From:  req.From,
		To:    req.Talkgroup,
		Group: true,
		// Both are arbitrary on the wire. The IP identification only has to
		// differ from one message to the next; the TMS reference read 0x84 to
		// 0x95 in every capture, climbing by one per message, so QSP counts
		// in that range rather than inventing a value nothing has sent.
		IPID:      uint16(rand.Uint32()),
		Reference: 0x80 | byte(s.textReference.Add(1)&0x7f),
		Text:      req.Text,
	}
	err := s.opts.Texts.SendText(hbp.RepeaterID(req.Peer), hbp.Timeslot(req.Timeslot), m)

	outcome, status := audit.OutcomeSuccess, http.StatusAccepted
	switch {
	case err == nil:
	case errors.Is(err, peers.ErrTextUnknownPeer):
		outcome, status = audit.OutcomeFailure, http.StatusNotFound
	case errors.Is(err, peers.ErrTextBusy), errors.Is(err, peers.ErrTextChannelBusy):
		outcome, status = audit.OutcomeFailure, http.StatusConflict
	case errors.Is(err, tms.ErrTooLong), errors.Is(err, tms.ErrPrivateNotYet):
		outcome, status = audit.OutcomeFailure, http.StatusBadRequest
	case errors.Is(err, peers.ErrTextNotListening):
		outcome, status = audit.OutcomeFailure, http.StatusServiceUnavailable
	default:
		outcome, status = audit.OutcomeFailure, http.StatusBadGateway
	}
	s.recordText(r, audit.ActionTextSent, req, outcome)
	if err != nil {
		writeJSON(w, s.log, status, map[string]string{"error": err.Error()})
		return
	}

	resp := map[string]string{
		"note": "Sending. It takes a little over a second to leave; the proof is the radio's display, " +
			"since nothing acknowledges a group text.",
	}
	if w := s.ownIDWarning(req.From); w != "" {
		resp["warning"] = w
	}
	writeJSON(w, s.log, status, resp)
}

// ownIDWarning notices a sender ID that was recently heard transmitting,
// which is probably somebody's radio. A radio commonly ignores a message
// that claims to come from itself, and that would look like a failure of
// the encoder rather than of the choice of ID.
func (s *Server) ownIDWarning(from uint32) string {
	if s.opts.Peers == nil {
		return ""
	}
	active, recent := s.opts.Peers.CallViews(time.Now())
	for _, c := range append(active, recent...) {
		if c.Source == from {
			return fmt.Sprintf("%d was heard transmitting recently, so it is probably a radio's own ID. "+
				"If that radio is the one you are watching, it may ignore a message from itself; "+
				"send from another ID to be sure.", from)
		}
	}
	return ""
}

func (s *Server) recordText(r *http.Request, action audit.Action, req sendTextRequest, outcome audit.Outcome) {
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
		Subject:    strconv.FormatUint(uint64(req.Peer), 10),
		Outcome:    outcome,
		SourceIP:   clientIP(r, s.opts.BehindProxy),
		Detail: map[string]string{
			"talkgroup": strconv.FormatUint(uint64(req.Talkgroup), 10),
			"timeslot":  strconv.Itoa(req.Timeslot),
			"from":      strconv.FormatUint(uint64(req.From), 10),
			"text":      req.Text,
		},
	}); err != nil {
		s.log.Warn("cannot record a text in the audit trail", "error", err)
	}
}
