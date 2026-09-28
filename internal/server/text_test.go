package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
)

type fakeTexts struct {
	err   error
	calls int
	peer  hbp.RepeaterID
	slot  hbp.Timeslot
	m     tms.Message
}

func (f *fakeTexts) SendText(peer hbp.RepeaterID, slot hbp.Timeslot, m tms.Message) error {
	f.calls++
	f.peer, f.slot, f.m = peer, slot, m
	return f.err
}

func newTextServer(t *testing.T, texts TextSender, src PeerSource, rec *recordingAudit) (*Server, *stubAuth) {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	a := newStubAuth()
	opts := Options{ListenAddress: "127.0.0.1:0", Auth: a, Texts: texts, Peers: src}
	if rec != nil {
		opts.Audit = rec
	}
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, a
}

const aSend = `{"peer":313291001,"timeslot":2,"talkgroup":2,"from":9990,"text":"QSP test"}`

// TestATextIsComposedAsTheFormAsked: the handler builds a group message from
// what was posted and hands it to the sender unchanged.
//
// To see it fail: drop Group: true from the message in handleSendText, and
// the sender is handed a private text.
func TestATextIsComposedAsTheFormAsked(t *testing.T) {
	f := &fakeTexts{}
	rec := &recordingAudit{}
	srv, a := newTextServer(t, f, nil, rec)
	resp := authed(t, srv, a, http.MethodPost, "/api/admin/text", aSend)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", resp.Code, resp.Body)
	}
	if f.calls != 1 || f.peer != 313291001 || f.slot != hbp.Timeslot2 {
		t.Fatalf("sender got %d calls, peer %d, %v", f.calls, f.peer, f.slot)
	}
	if !f.m.Group || f.m.To != 2 || f.m.From != 9990 || f.m.Text != "QSP test" {
		t.Errorf("message %+v", f.m)
	}
	if f.m.Reference < 0x80 {
		t.Errorf("reference %#x, outside the 0x80 range every capture used", f.m.Reference)
	}
	var body map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil || body["note"] == "" {
		t.Errorf("response %s", resp.Body)
	}
	if len(rec.events) == 0 || rec.events[len(rec.events)-1].Action != audit.ActionTextSent {
		t.Fatalf("no text.sent event was recorded: %+v", rec.events)
	}
	ev := rec.events[len(rec.events)-1]
	if ev.Outcome != audit.OutcomeSuccess || ev.Subject != "313291001" || ev.Detail["text"] != "QSP test" {
		t.Errorf("event %+v", ev)
	}
	if err := ev.Validate(); err != nil {
		t.Errorf("the event would be refused by the audit trail: %v", err)
	}
}

// TestAFormThatCannotBeAMessageIsRefusedBeforeSending: nothing reaches the
// sender, and the refusal says what to fix.
func TestAFormThatCannotBeAMessageIsRefusedBeforeSending(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no hotspot", `{"timeslot":2,"talkgroup":2,"from":9990,"text":"x"}`},
		{"timeslot three", `{"peer":1,"timeslot":3,"talkgroup":2,"from":9990,"text":"x"}`},
		{"no talkgroup", `{"peer":1,"timeslot":2,"from":9990,"text":"x"}`},
		{"a 25-bit sender", `{"peer":1,"timeslot":2,"talkgroup":2,"from":16777216,"text":"x"}`},
		{"nothing to say", `{"peer":1,"timeslot":2,"talkgroup":2,"from":9990,"text":"   "}`},
		{"a control character", `{"peer":1,"timeslot":2,"talkgroup":2,"from":9990,"text":"a\u0007b"}`},
		{"one character too many", fmt.Sprintf(`{"peer":1,"timeslot":2,"talkgroup":2,"from":9990,"text":%q}`,
			strings.Repeat("A", tms.MaxText+1))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeTexts{}
			srv, a := newTextServer(t, f, nil, nil)
			resp := authed(t, srv, a, http.MethodPost, "/api/admin/text", tc.body)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", resp.Code, resp.Body)
			}
			if f.calls != 0 {
				t.Error("the sender was called")
			}
		})
	}
	// And exactly the limit is accepted, so the page's number is the truth.
	f := &fakeTexts{}
	srv, a := newTextServer(t, f, nil, nil)
	body := fmt.Sprintf(`{"peer":1,"timeslot":2,"talkgroup":2,"from":9990,"text":%q}`, strings.Repeat("A", tms.MaxText))
	if resp := authed(t, srv, a, http.MethodPost, "/api/admin/text", body); resp.Code != http.StatusAccepted {
		t.Fatalf("%d characters: status %d: %s", tms.MaxText, resp.Code, resp.Body)
	}
}

// TestARefusedSendIsAuditedAndExplained maps each refusal the sender can
// make to a status, and records every one.
//
// To see a row fail: move recordText below the error return.
func TestARefusedSendIsAuditedAndExplained(t *testing.T) {
	tests := []struct {
		err  error
		code int
	}{
		{fmt.Errorf("%w: 1", peers.ErrTextUnknownPeer), http.StatusNotFound},
		{fmt.Errorf("%w: 1", peers.ErrTextBusy), http.StatusConflict},
		{fmt.Errorf("%w: 3155373 is transmitting", peers.ErrTextChannelBusy), http.StatusConflict},
		{fmt.Errorf("x: %w", tms.ErrTooLong), http.StatusBadRequest},
		{peers.ErrTextNotListening, http.StatusServiceUnavailable},
		{errors.New("something else"), http.StatusBadGateway},
	}
	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			rec := &recordingAudit{}
			srv, a := newTextServer(t, &fakeTexts{err: tc.err}, nil, rec)
			resp := authed(t, srv, a, http.MethodPost, "/api/admin/text", aSend)
			if resp.Code != tc.code {
				t.Fatalf("status %d, want %d: %s", resp.Code, tc.code, resp.Body)
			}
			if !strings.Contains(resp.Body.String(), "error") {
				t.Errorf("no error in %s", resp.Body)
			}
			if len(rec.events) == 0 {
				t.Fatal("nothing was audited")
			}
			if ev := rec.events[len(rec.events)-1]; ev.Action != audit.ActionTextSent || ev.Outcome != audit.OutcomeFailure {
				t.Errorf("event %+v", ev)
			}
		})
	}
}

// TestASenderThatIsARadioIsWarnedAbout: an ID heard transmitting is
// probably a radio's own, and the radio may ignore a text from itself.
func TestASenderThatIsARadioIsWarnedAbout(t *testing.T) {
	src := stubPeers{recent: []CallView{{Source: 9990, Target: 2, Group: true}}}
	srv, a := newTextServer(t, &fakeTexts{}, src, nil)
	resp := authed(t, srv, a, http.MethodPost, "/api/admin/text", aSend)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", resp.Code, resp.Body)
	}
	var body map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(body["warning"], "9990") {
		t.Errorf("no warning naming the ID: %s", resp.Body)
	}
}

// TestSendingATextNeedsASessionAndAListener: it is not public, and an
// instance with no DMR listener says why rather than failing obscurely.
func TestSendingATextNeedsASessionAndAListener(t *testing.T) {
	srv, _ := newTextServer(t, &fakeTexts{}, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/text", strings.NewReader(aSend))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without a session: %d, want 401", rec.Code)
	}

	srv, a := newTextServer(t, nil, nil, nil)
	if resp := authed(t, srv, a, http.MethodPost, "/api/admin/text", aSend); resp.Code != http.StatusServiceUnavailable {
		t.Errorf("with no listener: %d, want 503", resp.Code)
	}
	resp := authed(t, srv, a, http.MethodGet, "/api/admin", "")
	if !strings.Contains(resp.Body.String(), `"texts":{"available":false`) {
		t.Errorf("the admin page is not told the form is unavailable: %s", resp.Body)
	}
}
