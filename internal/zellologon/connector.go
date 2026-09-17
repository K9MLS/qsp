package zellologon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/k9mls/qsp/internal/health"
)

// ConnectorCheckTimeout bounds one read of qsp-zello's health.
const ConnectorCheckTimeout = 2 * time.Second

// connectorStatus is what qsp-zello serves on /healthz.
type connectorStatus struct {
	State       string `json:"state"`
	Since       string `json:"since"`
	Channel     string `json:"channel"`
	Detail      string `json:"detail"`
	Connections uint64 `json:"connections"`
}

// ConnectorChecker reports qsp-zello as the operator needs to read it: on the
// air, or what to do. It is the "zello" line of QSP's health.
//
// **This replaced a placeholder that outlived the work by five patches.** Until
// 2026-09-16 that line said the connector "arrives in phase 6" and "has not
// made its first connection", beneath two healthy lines showing Zello carrying
// calls both ways — exactly the staleness app_test.go's unbuilt list exists to
// prevent.
func ConnectorChecker(address string) health.Checker {
	client := &http.Client{Timeout: ConnectorCheckTimeout}
	return health.CheckerFunc{CheckName: "zello", Fn: func(ctx context.Context) health.Result {
		return checkConnector(ctx, client, address)
	}}
}

func checkConnector(ctx context.Context, client *http.Client, address string) health.Result {
	url := "http://" + address + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return health.Failing(fmt.Sprintf("cannot build a request for %s: %v", url, err),
			"check zello.connector_health")
	}
	resp, err := client.Do(req)
	if err != nil {
		return health.Degraded(fmt.Sprintf("qsp-zello is not answering at %s", address),
			"start it (sudo systemctl start qsp-zello); if it is running, its health_listen "+
				"in qsp-zello.json must match zello.connector_health")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var st connectorStatus
	if err == nil {
		err = json.Unmarshal(body, &st)
	}
	if err != nil || st.State == "" {
		return health.Degraded(fmt.Sprintf("something at %s answered, but not as qsp-zello", address),
			"zello.connector_health must be qsp-zello's health_listen and nothing else's")
	}

	detail := map[string]string{
		"state":       st.State,
		"connections": fmt.Sprintf("%d", st.Connections),
	}
	if st.Since != "" {
		detail["since"] = st.Since
	}
	since := st.Since
	if t, err := time.Parse(time.RFC3339, st.Since); err == nil {
		since = t.UTC().Format("2006-01-02 15:04 UTC")
	}
	withDetail := func(r health.Result) health.Result {
		r.Detail = detail
		return r
	}
	extra := ""
	if st.Detail != "" {
		extra = ": " + st.Detail
	}

	switch st.State {
	case "connected":
		return withDetail(health.Healthy(fmt.Sprintf("qsp-zello is connected to Zello channel %q since %s",
			st.Channel, since)))
	case "starting":
		return withDetail(health.Degraded("qsp-zello is starting", "give it a few seconds"))
	case "qsp_unreachable":
		return withDetail(health.Degraded("qsp-zello cannot reach QSP's logon socket"+extra,
			"restart qsp-zello; its logon_socket must match zello.logon_socket"))
	case "credentials_missing":
		return withDetail(health.Degraded("qsp-zello has no Zello credentials to log on with",
			"store the username, password and private key on the Zello page"))
	case "credentials_unusable":
		return withDetail(health.Degraded("the stored Zello credentials could not be used"+extra,
			"store them again on the Zello page"))
	case "zello_refused":
		return withDetail(health.Degraded("Zello refused the logon"+extra,
			"check the username and password, that the issuer matches the key, and that the "+
				"gateway account has joined the channel in the Zello app"))
	case "zello_unreachable":
		return withDetail(health.Degraded("qsp-zello cannot reach Zello's servers"+extra,
			"check this server's internet connection; qsp-zello keeps trying"))
	default:
		return withDetail(health.Degraded(fmt.Sprintf("qsp-zello reports %q%s", st.State, extra),
			"see journalctl -u qsp-zello"))
	}
}
