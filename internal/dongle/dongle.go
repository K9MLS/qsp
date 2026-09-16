// Package dongle reports on and controls the vocoder dongle's AMBEserver
// service, for the console's Zello page.
//
// # Why systemctl, and why so little of it
//
// QSP runs unprivileged and must stay that way. It reads the service's state
// with `systemctl show`, which any user may do, and starts, stops or restarts
// it with `systemctl`, which systemd refuses unless a polkit rule grants it —
// deploy/polkit/50-qsp-ambeserver.rules grants exactly those three verbs, on
// exactly one unit, to exactly the qsp user. The unit name is fixed here and
// never taken from a request, so the console cannot be used to act on any
// other service.
//
// The adapter is read from /sys rather than /dev: qsp.service has
// PrivateDevices, so /dev holds no serial ports for QSP to see, while
// /sys/bus/usb-serial describes every one and holds each adapter's latency
// timer.
package dongle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Unit is the one service this package reports on and controls.
const Unit = "ambeserver.service"

// Timeout bounds every systemctl call.
const Timeout = 5 * time.Second

// Runner runs a command and returns its standard output and standard error.
type Runner func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)

// ExecRunner runs real commands.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// Dongle reads and controls the service.
type Dongle struct {
	run     Runner
	sysRoot string
	lookup  func(string) (string, error)
}

// New returns a Dongle using real commands and the real /sys.
func New() *Dongle {
	return &Dongle{run: ExecRunner, sysRoot: "/sys", lookup: exec.LookPath}
}

// Adapter is one USB-serial adapter the kernel knows.
type Adapter struct {
	Name   string `json:"name"`   // ttyUSB0
	Driver string `json:"driver"` // ftdi_sio
	// LatencyMS is the FTDI latency timer, or -1 when the driver has none.
	LatencyMS int `json:"latency_ms"`
}

// Status is what the console shows.
type Status struct {
	// Managed is false where there is no systemctl — a container install —
	// and the service is run outside QSP.
	Managed bool `json:"managed"`
	// Installed is false when systemd does not know the unit.
	Installed bool      `json:"installed"`
	Active    string    `json:"active"` // active, inactive, failed, activating
	Sub       string    `json:"sub"`    // running, dead, ...
	Since     string    `json:"since,omitempty"`
	Adapters  []Adapter `json:"adapters"`
	// Problems are sentences an operator can act on.
	Problems []string `json:"problems,omitempty"`
}

// Status reads the service and the adapters.
func (d *Dongle) Status(ctx context.Context) Status {
	st := Status{Adapters: d.adapters()}
	for _, a := range st.Adapters {
		if a.Driver == "ftdi_sio" && a.LatencyMS > 1 {
			st.Problems = append(st.Problems, fmt.Sprintf(
				"%s's latency timer is %d ms; at more than 1 ms each vocoder exchange waits it out and "+
					"audio to Zello turns choppy. Install deploy/udev/99-ambe-dongle-latency.rules.",
				a.Name, a.LatencyMS))
		}
	}
	if len(st.Adapters) == 0 {
		st.Problems = append(st.Problems, "no USB-serial adapter is present; check the dongle is plugged in "+
			"and, on a virtual machine, passed through to it")
	}

	if _, err := d.lookup("systemctl"); err != nil {
		return st
	}
	st.Managed = true
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	out, _, err := d.run(ctx, "systemctl", "show", Unit, "--no-pager",
		"--property=LoadState,ActiveState,SubState,ActiveEnterTimestamp")
	if err != nil {
		st.Problems = append(st.Problems, fmt.Sprintf("cannot read %s: %v", Unit, err))
		return st
	}
	props := parseShow(out)
	st.Installed = props["LoadState"] == "loaded"
	st.Active, st.Sub = props["ActiveState"], props["SubState"]
	st.Since = props["ActiveEnterTimestamp"]
	if !st.Installed {
		st.Problems = append(st.Problems, "AMBEserver is not installed as a service; "+
			"docs/ZELLO.md, \"Running the dongle as a service\", has the steps")
	}
	return st
}

// ErrNotAuthorized is systemd refusing QSP a control action.
var ErrNotAuthorized = errors.New("QSP is not allowed to control " + Unit +
	"; install deploy/polkit/50-qsp-ambeserver.rules into /etc/polkit-1/rules.d/")

// ErrNotManaged is a control action where there is no systemctl.
var ErrNotManaged = errors.New("AMBEserver is not managed by systemd on this install")

// Verbs are the only actions allowed, matching the polkit rule.
var Verbs = []string{"start", "stop", "restart"}

// Control starts, stops or restarts the service.
func (d *Dongle) Control(ctx context.Context, verb string) error {
	allowed := false
	for _, v := range Verbs {
		if v == verb {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf("dongle: %q is not start, stop or restart", verb)
	}
	if _, err := d.lookup("systemctl"); err != nil {
		return ErrNotManaged
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	// --no-ask-password: without it, a refused action waits for a password
	// prompt nobody can answer, until the timeout.
	_, stderr, err := d.run(ctx, "systemctl", "--no-ask-password", verb, Unit)
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(stderr))
	if strings.Contains(msg, "Access denied") || strings.Contains(msg, "authentication required") ||
		strings.Contains(msg, "Interactive authentication") {
		return ErrNotAuthorized
	}
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("dongle: systemctl %s %s failed: %s", verb, Unit, msg)
}

func (d *Dongle) adapters() []Adapter {
	dir := filepath.Join(d.sysRoot, "bus", "usb-serial", "devices")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Adapter
	for _, e := range entries {
		base := filepath.Join(dir, e.Name())
		a := Adapter{Name: e.Name(), LatencyMS: -1}
		if target, err := os.Readlink(filepath.Join(base, "driver")); err == nil {
			a.Driver = filepath.Base(target)
		}
		if b, err := os.ReadFile(filepath.Join(base, "latency_timer")); err == nil {
			if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				a.LatencyMS = v
			}
		}
		out = append(out, a)
	}
	return out
}

func parseShow(b []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			m[k] = v
		}
	}
	return m
}
