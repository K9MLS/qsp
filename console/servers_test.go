package console_test

import (
	"strings"
	"testing"
)

// TestServersAreDrawnOnTheMapAndSaidToBeServers.
//
// The Overview's map drew the stations registered here and nothing else: not
// this server, and not a server it had dialled. A server that dialled this
// one was drawn, as though it were a hotspot. Each has to be read from the
// response and labelled, and the label has to say "server", because a pin of
// another shape is a cue somebody has to be taught.
//
// Break it: stop reading payload.servers in renderMap, or give a server's pin
// a station's label.
func TestServersAreDrawnOnTheMapAndSaidToBeServers(t *testing.T) {
	js := stripComments(readFile(t, "static/console.js"))
	mapJS := stripComments(readFile(t, "static/map.js"))
	css := stripComments(readFile(t, "static/console.css"))

	for _, want := range []struct{ in, text, why string }{
		{js, "payload.servers", "the servers in the response are read by nothing"},
		{js, "p.link_name", "a server that dialled this one is drawn as a hotspot"},
		{js, `"this server"`, "this server's own pin does not say it is this server"},
		{js, `kind: "server"`, "no point is marked as a server for the map to draw differently"},
		{mapJS, `p.kind === "server"`, "the map draws every point the same"},
		{mapJS, "map__pin--server", "a server's pin has no class of its own"},
		{css, ".map__pin--server", "the class a server's pin is given is not in the stylesheet"},
	} {
		if !strings.Contains(want.in, want.text) {
			t.Errorf("%s: %q is missing", want.why, want.text)
		}
	}

	html := readFile(t, "static/network.html")
	if !strings.Contains(html, "The\n            map is public") && !strings.Contains(html, "map is public") {
		t.Error("the Identity panel takes a position and does not say the map it goes on is public")
	}
}
