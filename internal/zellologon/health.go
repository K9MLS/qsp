package zellologon

import (
	"context"
	"fmt"
	"strings"

	"github.com/k9mls/qsp/internal/health"
)

// CheckName is the logon socket's health check.
const CheckName = "zello-logon"

// Checker reports whether a logon could be handed out now.
//
// **It separates the two things "the connector cannot log on" might mean**,
// which ADR-0062 requires: a credential nobody has entered is QSP's to say,
// while Zello refusing a logon it was handed is the connector's. Presence is
// checked with Has, so the check decrypts nothing.
func (s *Server) Checker(store Getter) health.Checker {
	return health.CheckerFunc{CheckName: CheckName, Fn: func(ctx context.Context) health.Result {
		served, peer, refused := s.Counters()
		detail := map[string]string{
			"socket":         s.opts.SocketPath,
			"served":         fmt.Sprintf("%d", served),
			"refused_peer":   fmt.Sprintf("%d", peer),
			"refused_logons": fmt.Sprintf("%d", refused),
		}
		s.mu.Lock()
		if s.lastErr != "" {
			detail["last_problem"] = s.lastErr
		}
		s.mu.Unlock()

		var missing []string
		for _, name := range []string{PrivateKeyName, UsernameName, PasswordName} {
			ok, err := store.Has(ctx, name)
			if err != nil {
				res := health.Failing(fmt.Sprintf("cannot read the credential store: %v", err),
					"check the database; the connector cannot be handed a logon until it is readable")
				res.Detail = detail
				return res
			}
			if !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			res := health.Degraded("the Zello credentials are not all entered",
				"add "+strings.Join(missing, ", ")+" in the console's credentials")
			res.Detail = detail
			return res
		}
		res := health.Healthy(fmt.Sprintf("ready to hand qsp-zello a logon; %d served", served))
		res.Detail = detail
		return res
	}}
}
