# Console design — proposal for discussion

**Status: proposal. Nothing here is built.**

The current console is honest, accessible and plain. It renders real data, has
first-class empty states, and passes contrast and keyboard checks. What it does
not do is look like the "premium radio operations console" the blueprint asks
for. It looks like a well-behaved admin page.

---

## What the design skill confirmed, and what it got wrong

**Confirmed.** The recommended palette for a real-time operations dashboard came
back as `#D97706` primary, `#0F172A` background, `#6366F1` accent, `#1F1E27`
muted — byte-for-byte what `tokens.css` already uses. The colour system needs no
change.

**Wrong.** It recommended **Cinzel / Josefin Sans**, a luxury real-estate
pairing. That is a mismatch for a console full of callsigns and radio IDs.
**Inter + Fira Code stays**: Fira Code's unambiguous `0`/`O` and `1`/`l` matter
more here than anything a display face offers, because an operator reading
`3132910` at a glance must not second-guess a digit.

Recommendations are inputs, not instructions.

---

## The constraint that shapes everything

**Motion and visual weight must depict something real.**

An RF console invites decoration: waterfalls, spectrum sweeps, pulsing rings.
Every one of those would be a lie in QSP, which has no spectrum data and no
radio hardware. A decorative sweep implies a capability that does not exist,
which is the same failure as fake data wearing a nicer coat.

So the rule for this redesign: **if it moves, it is because something happened.**

That is not a limitation. QSP has genuinely live data — datagrams arriving,
peers keying up, bridges opening on demand, hang timers counting down. A console
driven by those will feel more alive than one driven by decoration, and it will
be true.

---

## What is actually wrong now

1. **No hierarchy.** Four stacked panels of equal visual weight. Nothing says
   what to look at first.
2. **Nothing conveys "live".** A busy instance and an idle one look nearly
   identical until you read the numbers.
3. **Counters have no trend.** Six flat numbers. "1,482 datagrams" means nothing
   without knowing whether that is rising, steady or stopped.
4. **No spatial model.** QSP's whole job is connecting talkgroups across peers,
   and nothing on screen shows that shape.
5. **The nav is thin** now that most sections are built.

---

## Proposed direction

### 1. A status band that earns its space

Replace the single health pill with a band across the top: overall state, peer
count, active calls right now, bridges open, and **a live sparkline of frames
per second**.

The skill's guidance for real-time streaming data is a streaming area chart with
three requirements: a pause control, the current value as large readable text,
and a freeze under `prefers-reduced-motion`. All three are non-negotiable and
cheap.

It also suggested `#00FF00` for the live pulse; that clashes with the amber and
indigo system, so the existing healthy green (`#34d399`) is used instead.

**Honest because:** the sparkline is frames actually accepted per second. When
nothing is happening it is flat, and flat is the truth.

### 2. A bridge diagram

The one genuinely spatial thing QSP does is join talkgroups across peers, and it
is currently invisible. A compact diagram — peers down one side, talkgroups down
the other, a line for each bridge — would show at a glance what is linked to
what.

Lines are lit when the bridge is open, dimmed when closed, and pulse **only
while a transmission is crossing them**. A triggered bridge shows its hang-time
countdown on the line.

**Honest because:** every line is a configured bridge, and the pulse is a frame
that was actually relayed.

### 3. Last heard as a timeline

The current table is chronological but flat. A timeline with duration as bar
width shows the shape of activity — a net looks different from a kerchunk, and
a call that lost its terminator reads as a bar with a ragged end rather than a
tag in a column.

### 4. Depth through layering, not effects

The blueprint asks for cinematic and layered. That comes from a considered
elevation scale — surface, raised, overlay — plus generous whitespace and one
strong typographic jump between the display metric and its label. Not from
glass, glow or gradients, which the blueprint explicitly lists as anti-patterns.

### 5. Density that respects the phone

Net Control has to work on a phone at a hamfest. That means the desktop layout
can be dense and multi-column while the mobile layout is a single column
ordered by urgency: what is transmitting now, then what is connected, then
everything else. Mobile is not a shrunk desktop.

---

## What I would not do

- **No spectrum display, waterfall or signal meter.** QSP has no such data.
- **No world map of peers.** It would need geolocation QSP does not collect, and
  a hotspot's registered coordinates are frequently wrong.
- **No animated background.** Motion without meaning.
- **No dashboard-card spam.** Six cards of one number each is what the blueprint
  warns against; the counters belong in one band with trend, not scattered.

---

## Open questions

1. **Scope** — a full redesign, or the status band and bridge diagram first?
2. **Charting** — the skill recommends a library for streaming charts, but QSP
   has zero dependencies and no build step. A sparkline in hand-written SVG is
   perhaps eighty lines and keeps that property. Worth it?
3. **The bridge diagram** is the highest-value and highest-effort item. Is it
   worth doing properly, or is a table of bridges with an open/closed state
   enough for now?
4. **Is there an operator other than you?** Design for one expert and design for
   a club with three admins of varying confidence are different exercises.
