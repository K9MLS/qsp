// Package audit records administrative actions.
//
// Clubs run QSP with several administrators. When a routing change causes an
// argument at 20:05 on a Tuesday, the audit trail settles it. That is the whole
// purpose, and it drives two properties.
//
// The trail is append-only. Nothing in QSP updates or deletes an audit event; a
// correction is a new event.
//
// The trail never contains secrets. Recorders reject events whose detail keys
// look like credentials rather than trusting every caller to remember. Refusing
// to record is worse than recording a redaction, so the offending value is
// replaced and the event is kept.
package audit

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/logging"
)

// Action names an administrative operation.
//
// Actions are declared rather than free-form so that the console can filter by
// them and so a typo cannot create a category nobody ever searches.
type Action string

const (
	// ActionConfigChanged records a new active configuration version.
	ActionConfigChanged Action = "config.changed"
	// ActionConfigRolledBack records a rollback to an earlier version.
	ActionConfigRolledBack Action = "config.rolled_back"
	// ActionUserLogin records an authentication attempt.
	ActionUserLogin Action = "user.login"
	// ActionUserLogout records a session ending.
	ActionUserLogout Action = "user.logout"
	// ActionUserCreated records an administrator being made — by the setup
	// page for the first, or by another administrator afterwards.
	ActionUserCreated Action = "user.created"
	// ActionUserPasswordReset records one administrator resetting another's
	// password. **Not the password**, obviously, and not who it was given to;
	// the audit trail records that it happened and by whom.
	ActionUserPasswordReset Action = "user.password.reset"
	// ActionUserDeleted records an administrator being removed, along with
	// every session they held.
	ActionUserDeleted Action = "user.deleted"
	// ActionServiceStarted records process startup.
	ActionServiceStarted Action = "service.started"
	// ActionServiceStopped records graceful shutdown.
	ActionServiceStopped Action = "service.stopped"

	// ActionPeeringOffered records an invitation generated for another
	// network, naming the callsign and address it was made out to.
	//
	// # Why these three arrived late
	//
	// **All three were emitted for weeks and recorded none of the time.**
	// `recordPeering` took the action as a plain string, so "peering.offered"
	// compiled, vetted, passed staticcheck and was rejected at run time by
	// Record with a warning in the log that nobody was reading. SECURITY.md
	// stated as fact that accepting a peering writes an audit event naming the
	// far end whether it succeeds or fails, and ADR-0032 required it. Neither
	// was true, and the document was the only place it was written down.
	//
	// The string parameter is gone with them; an action is an Action now, so
	// the next one cannot be emitted without being declared here first.
	ActionPeeringOffered Action = "peering.offered"
	// ActionPeeringAccepted records a peering agreed and written into the
	// configuration, or refused.
	ActionPeeringAccepted Action = "peering.accepted"
	// ActionPeeringRemoved records a link removed from the configuration.
	ActionPeeringRemoved Action = "peering.removed"

	// # The eight below were emitted and never recorded, until 2026-09-16
	//
	// **The same defect as the three above, a second time.** The console's
	// credential, backup and peer-credential handlers passed their actions as
	// string literals, and an untyped string literal converts to Action
	// without complaint — so changing the parameter's type did not catch
	// them. Every one was refused by Validate with a warning in the log:
	// every credential stored or removed, every backup taken or restored and
	// every peer credential issued or revoked since those handlers were
	// written. A test in internal/server now reads the source and refuses a
	// literal passed to an audit helper.

	// ActionSecretSet records a credential stored or replaced, by name only.
	ActionSecretSet Action = "secret.set"
	// ActionSecretRemoved records a credential removed, by name only.
	ActionSecretRemoved Action = "secret.removed"
	// ActionPeerCredentialIssued records a per-peer password issued.
	ActionPeerCredentialIssued Action = "peer.credential.issued"
	// ActionPeerCredentialRevoked records a per-peer password revoked.
	ActionPeerCredentialRevoked Action = "peer.credential.revoked"
	// ActionConfigExported records the shareable configuration downloaded.
	ActionConfigExported Action = "config.exported"
	// ActionConfigRestored records a shareable configuration restored.
	ActionConfigRestored Action = "config.restored"
	// ActionConfigFullExported records the encrypted full backup taken.
	ActionConfigFullExported Action = "config.full_export"
	// ActionConfigFullRestored records the encrypted full backup restored.
	ActionConfigFullRestored Action = "config.full_restored"

	// ActionDongleControlled records the vocoder dongle's AMBEserver service
	// started, stopped or restarted from the console; the subject is the verb.
	ActionDongleControlled Action = "dongle.controlled"
)

var knownActions = map[Action]bool{
	ActionConfigChanged:     true,
	ActionConfigRolledBack:  true,
	ActionUserLogin:         true,
	ActionUserLogout:        true,
	ActionUserCreated:       true,
	ActionUserPasswordReset: true,
	ActionUserDeleted:       true,
	ActionServiceStarted:    true,
	ActionServiceStopped:    true,
	ActionPeeringOffered:    true,
	ActionPeeringAccepted:   true,
	ActionPeeringRemoved:    true,

	ActionSecretSet:             true,
	ActionSecretRemoved:         true,
	ActionPeerCredentialIssued:  true,
	ActionPeerCredentialRevoked: true,
	ActionConfigExported:        true,
	ActionConfigRestored:        true,
	ActionConfigFullExported:    true,
	ActionConfigFullRestored:    true,
	ActionDongleControlled:      true,
}

// IsKnownAction reports whether a is a declared action.
func IsKnownAction(a Action) bool { return knownActions[a] }

// Outcome is the result of an audited action.
type Outcome string

const (
	// OutcomeSuccess means the action completed.
	OutcomeSuccess Outcome = "success"
	// OutcomeFailure means the action was attempted and did not complete.
	OutcomeFailure Outcome = "failure"
	// OutcomeDenied means the action was refused by authorisation.
	OutcomeDenied Outcome = "denied"
)

// SystemActor is the actor recorded for actions QSP takes on its own behalf,
// such as startup and shutdown.
const SystemActor = "system"

// Event is one administrative action.
type Event struct {
	// OccurredAt is when the action happened, in UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// Actor is a username, or SystemActor. It is never a credential or a
	// session token.
	Actor string `json:"actor"`
	// Action is what was done.
	Action Action `json:"action"`
	// Subject identifies what it was done to, for example a configuration
	// version number or a peer ID. It may be empty.
	Subject string `json:"subject,omitempty"`
	// Outcome is the result.
	Outcome Outcome `json:"outcome"`
	// SourceIP is the client address, when the action came over the network.
	SourceIP string `json:"source_ip,omitempty"`
	// Detail carries supporting values. Keys resembling credentials are
	// redacted before the event is recorded.
	Detail map[string]string `json:"detail,omitempty"`
}

// sensitiveKeySubstrings identify detail keys whose values must never be
// recorded. The match is case-insensitive and substring-based, which is
// deliberately over-eager: a redacted value that did not need redacting costs
// nothing, while a leaked credential is unrecoverable.
var sensitiveKeySubstrings = []string{
	"password", "passwd", "secret", "token", "key", "credential",
	"authorization", "auth", "cookie", "session", "signature", "passphrase",
}

// IsSensitiveKey reports whether a detail key must have its value redacted.
func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeySubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// Redact returns a copy of detail with sensitive values replaced.
//
// The key is retained so that an operator can see a credential was involved
// without learning its value.
func Redact(detail map[string]string) map[string]string {
	if len(detail) == 0 {
		return nil
	}
	out := make(map[string]string, len(detail))
	for k, v := range detail {
		if IsSensitiveKey(k) {
			out[k] = logging.RedactedPlaceholder
			continue
		}
		out[k] = v
	}
	return out
}

// Validate reports whether e is well formed.
func (e Event) Validate() error {
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("audit event has no timestamp")
	}
	if strings.TrimSpace(e.Actor) == "" {
		return fmt.Errorf("audit event has no actor: use the username, or %q for actions QSP takes itself", SystemActor)
	}
	if !IsKnownAction(e.Action) {
		return fmt.Errorf("audit event has undeclared action %q: declare it in the audit package before using it", e.Action)
	}
	switch e.Outcome {
	case OutcomeSuccess, OutcomeFailure, OutcomeDenied:
	default:
		return fmt.Errorf("audit event has unrecognised outcome %q: use success, failure or denied", e.Outcome)
	}
	return nil
}

// Recorder persists audit events.
//
// Record must not fail silently, and must not prevent the audited action from
// having happened; callers log a recording failure rather than rolling back.
type Recorder interface {
	Record(ctx context.Context, e Event) error
}

// LogRecorder writes audit events to the structured log.
//
// It is the recorder used before persistent storage is available, and remains
// useful alongside it: an operator shipping logs off the box gets the audit
// trail without database access.
type LogRecorder struct {
	log *slog.Logger
}

// NewLogRecorder constructs a LogRecorder.
func NewLogRecorder(log *slog.Logger) *LogRecorder {
	return &LogRecorder{log: logging.Subsystem(log, "audit")}
}

// Record implements Recorder.
//
// Sensitive detail values are redacted before writing. An invalid event is
// rejected rather than written in a form the console cannot later filter.
func (r *LogRecorder) Record(_ context.Context, e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}

	attrs := []any{
		slog.Time("occurred_at", e.OccurredAt.UTC()),
		slog.String("actor", e.Actor),
		slog.String("action", string(e.Action)),
		slog.String("outcome", string(e.Outcome)),
	}
	if e.Subject != "" {
		attrs = append(attrs, slog.String("subject", e.Subject))
	}
	if e.SourceIP != "" {
		attrs = append(attrs, slog.String("source_ip", e.SourceIP))
	}

	if safe := Redact(e.Detail); len(safe) > 0 {
		keys := make([]string, 0, len(safe))
		for k := range safe {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		detailAttrs := make([]any, 0, len(keys))
		for _, k := range keys {
			detailAttrs = append(detailAttrs, slog.String(k, safe[k]))
		}
		attrs = append(attrs, slog.Group("detail", detailAttrs...))
	}

	r.log.Info("audit", attrs...)
	return nil
}
