package config

import (
	"encoding/json"
	"reflect"
	"strings"
)

// When a saved setting takes effect.
//
// **Every setting is in one of these two lists, and a test fails when one is
// in neither.** NeedsRestart used to be a list of comparisons written by hand,
// one per setting somebody remembered, and a setting nobody remembered was
// reported as in force the moment it was saved. Ten such were found on
// 2026-10-07 (section G): everything under `p25`, forwarding, Zello, the
// transcoders, the unlink talkgroup, call retention, what a link is told about
// this server. Each was a save that said yes and did nothing.
//
// A setting is named by its path in the file. A name covers everything below
// it, so `dmr.parrot` is the five settings of the parrot, and a setting added
// to it later is covered without anybody remembering to add it.
//
// **A setting in neither list is treated as needing a restart.** Told to
// restart for nothing is an annoyance; told a change is live when it is not is
// the fault this file exists to end.

// appliedOnSave are the settings a running server takes up when they are
// saved. Each is here because something carries it there, and that is named
// beside it. **Putting a name here is a claim about the program**, and the
// claim is only as good as the carrying: add the setter first.
var appliedOnSave = []string{
	// Not a setting: the format of the file.
	"version",

	// server.Server.ApplyConfig, through the configuration manager.
	"server.map",
	"dmr.join.address",
	"dmr.join.talkgroups",
	// auth.Service.SetSessionLifetime. Sessions already issued keep the end
	// they were given.
	"server.session_lifetime",

	// peers.Listener.Apply: the routing table, the triggers and the schedule,
	// on the goroutine that owns the routing core.
	"dmr.bridges",
	"dmr.triggers",
	"dmr.schedule",
	// peers.Master.SetSubscription.
	"dmr.subscription.enabled",
	"dmr.subscription.timeout",
	"dmr.subscription.static",
	// routing.Core.SetAccess and peers.Master.SetAccess.
	"dmr.access",

	// ipsclink.Listener.SetAllowedPeers and SetPeerNames.
	"ipsc.allowed_peers",
	"ipsc.peer_names",
	// p25link.Listener.SetAllowedCallsigns. A gateway taken off the list is
	// refused at its next poll.
	"p25.allowed_callsigns",

	// weather.Service.Apply.
	"weather",
}

// appliedAtStart are the settings read once, when the process starts.
var appliedAtStart = []string{
	// Written once, by the server, and never changed from a page.
	"server.identifier",

	// The console's own socket.
	"server.listen_address",
	"server.read_header_timeout",
	"server.read_timeout",
	"server.write_timeout",
	"server.idle_timeout",
	"server.shutdown_timeout",
	"server.behind_proxy",

	"database",
	"logging",
	"events",

	// The DMR listener, its master and its password sources.
	"dmr.enabled",
	"dmr.listen_address",
	"dmr.password_file",
	// The directory is given to the master when it is built. A server whose
	// first link offer created one kept refusing the far end, with the
	// password it had just been issued, until it was restarted.
	"dmr.peer_passwords",
	"dmr.peer_timeout",
	"dmr.login_timeout",
	"dmr.max_peers",
	"dmr.subscriber_timeout",
	// The routing core is built only when this is on, so it can be neither
	// started nor stopped under a running listener.
	"dmr.forwarding",
	// Given to the listener when it is built.
	"dmr.subscription.unlink",
	"dmr.subscription.unlink_timeslot",
	// The recorder is built once and handed to the listener.
	"dmr.parrot",
	// Links hold sockets and a handshake; vocoder channels hold a device.
	"dmr.upstreams",
	"dmr.transcoders",
	// The resolver and its fetcher are built once, with the contact in them.
	"dmr.callsigns",
	// Given to both call stores when they are opened.
	"dmr.calls",

	"ipsc.enabled",
	"ipsc.listen_address",
	"ipsc.master_id",
	"ipsc.peer_timeout_seconds",
	"ipsc.colour_code",
	"ipsc.slot_bit_is_timeslot2",

	"p25.enabled",
	"p25.listen_address",
	"p25.callsign",
	"p25.max_gateways",

	// The logon socket is opened once, with the issuer, audience and channel
	// in it, and the connector's health address is registered once.
	"zello",

	"p25_repeaters",
}

// toldToLinks are what this server says about itself: its name, its callsign
// and where it is.
//
// **Applied on save everywhere but one place.** The join page, this server's
// own pin and what it answers a server that links to it all follow a save. A
// link this server dials was built with them and announces them when it logs
// in, so with such a link running a change reaches the far end at a restart
// and not before: the pin moved on this server's map and stayed where it was
// on its neighbour's. With no such link there is nobody left to tell, and
// naming a restart would be asking for one for nothing.
var toldToLinks = []string{
	"dmr.identity",
	"dmr.join.network_name",
}

// dialsALink reports whether cfg has a link that announces this server.
func dialsALink(cfg Config) bool {
	for _, u := range cfg.DMR.Upstreams {
		if u.Enabled && u.HomebrewProtocol() {
			return true
		}
	}
	return false
}

// AppliedOnSave reports whether a running server takes up a change to this
// setting without being restarted, and whether the setting is known at all.
func AppliedOnSave(path string) (live, known bool) {
	if _, ok := covering(appliedOnSave, path); ok {
		return true, true
	}
	if _, ok := covering(toldToLinks, path); ok {
		return true, true
	}
	_, ok := covering(appliedAtStart, path)
	return false, ok
}

// covering returns the name in list that is path or is above it.
func covering(list []string, path string) (string, bool) {
	for _, name := range list {
		if path == name || strings.HasPrefix(path, name+".") {
			return name, true
		}
	}
	return "", false
}

// NeedsRestart lists the settings that differ between two configurations and
// cannot take effect until the process is restarted.
//
// **It returns names rather than a boolean**, because "restart required" tells
// an operator to interrupt their network without saying what for, and they will
// reasonably want to know whether it can wait until the net is over.
//
// Each is named by its path in the file, in the order of the file: the
// setting itself and not the group it is in, so that "p25.callsign" is what
// the operator reads and not "p25".
func NeedsRestart(before, after Config) []string {
	was, is := Settings(before), Settings(after)
	// The link that was built is the one that has to be told, so it is the
	// configuration before the change that decides.
	announces := dialsALink(before)

	var fields []string
	for _, path := range SettingPaths() {
		if was[path] == is[path] {
			continue
		}
		if _, live := covering(appliedOnSave, path); live {
			continue
		}
		if _, told := covering(toldToLinks, path); told && !announces {
			continue
		}
		fields = append(fields, path)
	}
	return fields
}

// SettingPaths is every setting a configuration has, by its path in the file,
// in the order of the file.
func SettingPaths() []string {
	var out []string
	walkSettings(reflect.ValueOf(Config{}), "", func(path string, _ reflect.Value) {
		out = append(out, path)
	})
	return out
}

// Settings is the value of every setting in cfg, by path, in a form two of
// which are equal exactly when the settings are.
//
// **A list with nothing in it is one value however it is spelled.** A
// configuration read from a file has `[]` where one built in the program has
// nothing, and reporting that as a change would name a restart for a save
// that changed nothing.
func Settings(cfg Config) map[string]string {
	// The hold is the one setting whose absence has a value: left out, it is
	// the default, and a server told to restart for the default spelled out
	// would be told so for nothing.
	held := int(cfg.P25Repeaters.Hold().Milliseconds())
	cfg.P25Repeaters.HoldMS = &held

	out := map[string]string{}
	walkSettings(reflect.ValueOf(cfg), "", func(path string, v reflect.Value) {
		switch v.Kind() {
		case reflect.Slice, reflect.Map:
			if v.Len() == 0 {
				out[path] = "[]"
				return
			}
		}
		b, err := json.Marshal(v.Interface())
		if err != nil {
			// Nothing in a configuration fails to encode, since it is read
			// from JSON. Were that to change, a value that cannot be compared
			// is reported as changed.
			out[path] = "unreadable: " + err.Error()
			return
		}
		out[path] = string(b)
	})
	return out
}

// walkSettings calls visit for every setting under v, by its name in the file.
//
// A setting is anything that is not a group of settings: a number, a piece of
// text, a list. A group that is absent is walked as an empty one, so its
// settings are found whether or not a configuration has it.
func walkSettings(v reflect.Value, path string, visit func(string, reflect.Value)) {
	t := v.Type()
	if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
		if v.IsNil() {
			v = reflect.Zero(t.Elem())
		} else {
			v = v.Elem()
		}
		t = v.Type()
	}
	if t.Kind() != reflect.Struct {
		visit(path, v)
		return
	}
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if path != "" {
			name = path + "." + name
		}
		walkSettings(v.Field(i), name, visit)
	}
}
