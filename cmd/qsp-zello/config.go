//go:build zello

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// Config is the connector's own configuration. **It holds no secret and
// nothing an operator changes**: the logon and the channel come from QSP
// (ADR-0066), set on its Zello page. This file says only where to reach QSP.
type Config struct {
	// LogonSocket is QSP's zello.logon_socket.
	LogonSocket string `json:"logon_socket"`
	// Endpoint is the WebSocket URL. Empty selects the consumer service.
	Endpoint string `json:"endpoint,omitempty"`
	// USRPListen is where audio from QSP arrives: QSP's usrp_peer.
	USRPListen string `json:"usrp_listen"`
	// USRPPeer is QSP's usrp_listen, where audio for QSP is sent.
	USRPPeer string `json:"usrp_peer"`
	// HealthListen serves /healthz. Empty serves nothing.
	HealthListen string `json:"health_listen,omitempty"`
}

// loadConfig reads and validates a configuration, refusing unknown fields for
// the same reason QSP does: a misspelled setting silently ignored is a setting
// an operator believes is in effect.
func loadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s is not usable: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("%s is not usable: %w", path, err)
	}
	return c, nil
}

func (c Config) validate() error {
	var problems []string
	if !filepath.IsAbs(strings.TrimSpace(c.LogonSocket)) {
		problems = append(problems, "logon_socket must be an absolute path: QSP's zello.logon_socket")
	}
	if e := strings.TrimSpace(c.Endpoint); e != "" && !strings.HasPrefix(e, "wss://") {
		// Refused here as well as by the session, so it is a configuration
		// error at -check rather than a connection retried forever.
		problems = append(problems, "endpoint must be wss://; a plain connection would send the "+
			"password in the clear")
	}
	listen, lerr := netip.ParseAddrPort(c.USRPListen)
	if lerr != nil {
		problems = append(problems, fmt.Sprintf("usrp_listen %q is not an IP:port; it is QSP's usrp_peer", c.USRPListen))
	}
	peer, perr := netip.ParseAddrPort(c.USRPPeer)
	switch {
	case perr != nil:
		problems = append(problems, fmt.Sprintf("usrp_peer %q is not an IP:port; it is QSP's usrp_listen", c.USRPPeer))
	case peer.Addr().IsUnspecified() || peer.Port() == 0:
		problems = append(problems, "usrp_peer must name QSP's own address and port; USRP has no "+
			"authentication and this is what every datagram is checked against")
	case lerr == nil && listen == peer:
		problems = append(problems, "usrp_peer is the same as usrp_listen")
	}
	if h := strings.TrimSpace(c.HealthListen); h != "" {
		if ap, err := netip.ParseAddrPort(h); err != nil {
			problems = append(problems, fmt.Sprintf("health_listen %q is not an IP:port", h))
		} else if !ap.Addr().IsLoopback() {
			problems = append(problems, "health_listen must be a loopback address; the report names "+
				"the channel and the reason a logon was refused")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}
