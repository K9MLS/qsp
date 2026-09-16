package dongle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSys builds /sys/bus/usb-serial/devices with the given adapters.
func fakeSys(t *testing.T, adapters map[string]struct {
	driver  string
	latency string
}) string {
	t.Helper()
	root := t.TempDir()
	dev := filepath.Join(root, "bus", "usb-serial", "devices")
	drivers := filepath.Join(root, "bus", "usb-serial", "drivers")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, a := range adapters {
		d := filepath.Join(dev, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if a.driver != "" {
			target := filepath.Join(drivers, a.driver)
			_ = os.MkdirAll(target, 0o755)
			if err := os.Symlink(target, filepath.Join(d, "driver")); err != nil {
				t.Fatal(err)
			}
		}
		if a.latency != "" {
			if err := os.WriteFile(filepath.Join(d, "latency_timer"), []byte(a.latency+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

type call struct{ args []string }

func fakeDongle(sysRoot string, haveSystemctl bool, run Runner) (*Dongle, *[]call) {
	calls := &[]call{}
	d := &Dongle{sysRoot: sysRoot,
		lookup: func(string) (string, error) {
			if haveSystemctl {
				return "/usr/bin/systemctl", nil
			}
			return "", errors.New("not found")
		},
		run: func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
			*calls = append(*calls, call{args: append([]string{name}, args...)})
			return run(ctx, name, args...)
		}}
	return d, calls
}

func show(props string) Runner {
	return func(context.Context, string, ...string) ([]byte, []byte, error) {
		return []byte(props), nil, nil
	}
}

// TestTheStatusSaysWhatAnOperatorNeedsToAct.
//
// To see rows fail: drop the latency check in Status and "a dongle at the
// default latency" raises no problem, which is the choppy-audio defect of
// 2026-09-16 going unmentioned; or read LoadState wrongly and "not installed"
// reads installed.
func TestTheStatusSaysWhatAnOperatorNeedsToAct(t *testing.T) {
	type ad = struct {
		driver  string
		latency string
	}
	running := "LoadState=loaded\nActiveState=active\nSubState=running\nActiveEnterTimestamp=Wed 2026-09-16 19:26:41 UTC\n"
	tests := []struct {
		name          string
		adapters      map[string]ad
		systemctl     bool
		show          string
		wantManaged   bool
		wantInstalled bool
		wantActive    string
		wantProblem   string // empty means no problems
	}{
		{"a healthy install", map[string]ad{"ttyUSB0": {"ftdi_sio", "1"}}, true, running, true, true, "active", ""},
		{"a dongle at the default latency", map[string]ad{"ttyUSB0": {"ftdi_sio", "16"}}, true, running, true, true, "active", "latency timer is 16 ms"},
		{"no adapter", nil, true, running, true, true, "active", "no USB-serial adapter"},
		{"not installed as a service", map[string]ad{"ttyUSB0": {"ftdi_sio", "1"}}, true,
			"LoadState=not-found\nActiveState=inactive\nSubState=dead\n", true, false, "inactive", "not installed as a service"},
		{"a container install with no systemctl", map[string]ad{"ttyUSB0": {"ftdi_sio", "1"}}, false, "", false, false, "", ""},
		{"the start limit tripped", map[string]ad{"ttyUSB0": {"ftdi_sio", "1"}}, true,
			"LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=start-limit-hit\n", true, true, "failed",
			"started too many times"},
		{"an ordinary failure is not called a tripped limit", map[string]ad{"ttyUSB0": {"ftdi_sio", "1"}}, true,
			"LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=exit-code\n", true, true, "failed", ""},
		{"a non-FTDI adapter has no latency to judge", map[string]ad{"ttyUSB0": {"cp210x", ""}}, true, running, true, true, "active", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := fakeDongle(fakeSys(t, tc.adapters), tc.systemctl, show(tc.show))
			st := d.Status(context.Background())
			if st.Managed != tc.wantManaged || st.Installed != tc.wantInstalled || st.Active != tc.wantActive {
				t.Errorf("managed %v installed %v active %q, want %v %v %q", st.Managed, st.Installed, st.Active,
					tc.wantManaged, tc.wantInstalled, tc.wantActive)
			}
			joined := strings.Join(st.Problems, " | ")
			if tc.wantProblem == "" && joined != "" {
				t.Errorf("problems %q, want none", joined)
			}
			if tc.wantProblem != "" && !strings.Contains(joined, tc.wantProblem) {
				t.Errorf("problems %q do not mention %q", joined, tc.wantProblem)
			}
		})
	}
}

// TestControlActsOnOneUnitWithThreeVerbs: the console must not become a way to
// act on any other service, or to do anything to this one but start, stop and
// restart it.
//
// To see it bite: let Control pass any verb through, and "enable" reaches
// systemctl.
func TestControlActsOnOneUnitWithThreeVerbs(t *testing.T) {
	root := fakeSys(t, nil)
	tests := []struct {
		verb    string
		stderr  string
		wantErr error
		wantRun bool
	}{
		{"restart", "", nil, true},
		{"stop", "", nil, true},
		{"enable", "", nil, false},
		{"restart --now; reboot", "", nil, false},
		{"start", "Failed to start ambeserver.service: Access denied", ErrNotAuthorized, true},
		{"start", "Failed to start ambeserver.service: Interactive authentication required.", ErrNotAuthorized, true},
	}
	for _, tc := range tests {
		t.Run(tc.verb, func(t *testing.T) {
			d, calls := fakeDongle(root, true, func(context.Context, string, ...string) ([]byte, []byte, error) {
				if tc.stderr != "" {
					return nil, []byte(tc.stderr), errors.New("exit status 1")
				}
				return nil, nil, nil
			})
			err := d.Control(context.Background(), tc.verb)
			ran := len(*calls) > 0
			if ran != tc.wantRun {
				t.Fatalf("systemctl ran = %v, want %v", ran, tc.wantRun)
			}
			if !tc.wantRun {
				if err == nil {
					t.Error("an unlisted verb was accepted")
				}
				return
			}
			args := strings.Join((*calls)[0].args, " ")
			if args != "systemctl --no-ask-password "+tc.verb+" "+Unit {
				t.Errorf("ran %q", args)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("error %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && err != nil {
				t.Errorf("error %v", err)
			}
		})
	}
}

// TestNoSystemctlIsNotManagedRatherThanAFailure.
func TestNoSystemctlIsNotManagedRatherThanAFailure(t *testing.T) {
	d, calls := fakeDongle(fakeSys(t, nil), false, show(""))
	if err := d.Control(context.Background(), "restart"); !errors.Is(err, ErrNotManaged) {
		t.Errorf("error %v, want ErrNotManaged", err)
	}
	if len(*calls) != 0 {
		t.Error("ran a command with no systemctl present")
	}
}

// TestResetClearsTheFailureThenStarts: the page's "Reset and start", which is
// what brought AMBEserver back on 2026-09-16.
//
// To see it bite: map "reset" to a bare start, and systemd refuses it for as
// long as the limit holds.
func TestResetClearsTheFailureThenStarts(t *testing.T) {
	d, calls := fakeDongle(fakeSys(t, nil), true, func(context.Context, string, ...string) ([]byte, []byte, error) {
		return nil, nil, nil
	})
	if err := d.Control(context.Background(), "reset"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range *calls {
		got = append(got, strings.Join(c.args, " "))
	}
	want := []string{"systemctl --no-ask-password reset-failed " + Unit, "systemctl --no-ask-password start " + Unit}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// TestALimitHitStatusIsFlagged: LimitHit is what shows the reset button.
func TestALimitHitStatusIsFlagged(t *testing.T) {
	d, _ := fakeDongle(fakeSys(t, nil), true, show("LoadState=loaded\nActiveState=failed\nResult=start-limit-hit\n"))
	if !d.Status(context.Background()).LimitHit {
		t.Error("a tripped start limit is not flagged, so the page offers no way out")
	}
}
