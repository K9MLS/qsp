//go:build zello

package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// healthHandler serves the connector's state.
//
// 200 only when connected. **The state names the action**: credentials_missing
// sends an operator to QSP's console, zello_refused to Zello's, qsp_unreachable
// to QSP itself — ADR-0062's requirement that "the transcoder is down" and "the
// credential is wrong" never look alike.
func (c *connector) healthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		body := map[string]any{
			"state":                  c.state,
			"since":                  c.since.UTC().Format(time.RFC3339),
			"channel":                c.channel,
			"connections":            c.connected.Load(),
			"usrp_discarded_offline": c.discarded.Load(),
		}
		if c.lost != nil {
			// Audio given up because Zello was not taking it as fast as QSP
			// sent it. Anything but zero is a connection to Zello that stalled.
			body["usrp_dropped_queue_full"] = c.lost()
		}
		if c.detail != "" {
			body["detail"] = c.detail
		}
		status := http.StatusServiceUnavailable
		if c.state == stateConnected {
			status = http.StatusOK
		}
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
	return mux
}
