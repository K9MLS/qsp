package hbp

import "fmt"

// Wire sizes of the fixed-length control messages, as observed in
// testdata/hbp/hbp-login-session.pcap.
const (
	loginSize       = 8  // "RPTL" + id(4)
	ackSize         = 10 // "RPTACK" + payload(4)
	keySize         = 40 // "RPTK" + id(4) + digest(32)
	pingSize        = 11 // "RPTPING" + id(4)
	pongSize        = 11 // "MSTPONG" + id(4)
	gatewayPongSize = 4  // "DMRP"

	// DigestSize is the length of the authentication digest, which is SHA-256.
	DigestSize = 32
)

// Login is a peer's initial login request. Tag "RPTL".
//
// It carries only the repeater ID; the master replies with Ack containing a
// salt.
type Login struct {
	RepeaterID RepeaterID
}

// Kind implements Message.
func (Login) Kind() Kind { return KindLogin }

// Marshal implements Message.
func (m Login) Marshal() []byte { return m.AppendTo(make([]byte, 0, loginSize)) }

// AppendTo implements Message.
func (m Login) AppendTo(dst []byte) []byte {
	dst = append(dst, "RPTL"...)
	var id [4]byte
	putID(id[:], m.RepeaterID)
	return append(dst, id[:]...)
}

func parseLogin(b []byte) (Message, error) {
	if err := exactLen(b, loginSize, KindLogin); err != nil {
		return nil, err
	}
	return Login{RepeaterID: getID(b[4:8])}, nil
}

// Ack is a master's acknowledgement. Tag "RPTACK".
//
// # The ambiguity
//
// Ack carries four bytes whose meaning depends entirely on connection state,
// and nothing in the packet distinguishes the two cases:
//
//   - Immediately after Login, the payload is a random salt to be hashed with
//     the shared password. Use Salt.
//   - After Key or Config, the payload is the peer's own repeater ID, echoed
//     back as confirmation. Use RepeaterID.
//
// In the captured session the same ten-byte shape carried salt 0x9947b430 and,
// twice, repeater ID 3132910. A parser cannot choose between them and this one
// does not try; interpretation belongs to the peer state machine, which knows
// what it last sent.
type Ack struct {
	// Payload is the four bytes exactly as they appeared on the wire.
	Payload [4]byte
}

// Kind implements Message.
func (Ack) Kind() Kind { return KindAck }

// Marshal implements Message.
func (m Ack) Marshal() []byte { return m.AppendTo(make([]byte, 0, ackSize)) }

// AppendTo implements Message.
func (m Ack) AppendTo(dst []byte) []byte {
	dst = append(dst, "RPTACK"...)
	return append(dst, m.Payload[:]...)
}

// Salt interprets the payload as an authentication salt.
//
// Valid only for the Ack that immediately follows Login.
func (m Ack) Salt() [4]byte { return m.Payload }

// RepeaterID interprets the payload as an echoed repeater ID.
//
// Valid only for an Ack that follows Key or Config.
func (m Ack) RepeaterID() RepeaterID { return getID(m.Payload[:]) }

func parseAck(b []byte) (Message, error) {
	if err := exactLen(b, ackSize, KindAck); err != nil {
		return nil, err
	}
	var m Ack
	copy(m.Payload[:], b[6:10])
	return m, nil
}

// Key is a peer's authentication response. Tag "RPTK".
//
// Digest is SHA-256 over the salt from Ack concatenated with the shared
// password. See Digest for the construction and its verification.
type Key struct {
	RepeaterID RepeaterID
	Digest     [DigestSize]byte
}

// Kind implements Message.
func (Key) Kind() Kind { return KindKey }

// Marshal implements Message.
func (m Key) Marshal() []byte { return m.AppendTo(make([]byte, 0, keySize)) }

// AppendTo implements Message.
func (m Key) AppendTo(dst []byte) []byte {
	dst = append(dst, "RPTK"...)
	var id [4]byte
	putID(id[:], m.RepeaterID)
	dst = append(dst, id[:]...)
	return append(dst, m.Digest[:]...)
}

func parseKey(b []byte) (Message, error) {
	if err := exactLen(b, keySize, KindKey); err != nil {
		return nil, err
	}
	m := Key{RepeaterID: getID(b[4:8])}
	copy(m.Digest[:], b[8:40])
	return m, nil
}

// Ping is a peer's keepalive. Tag "RPTPING".
//
// Observed every 10 seconds on the master link.
type Ping struct {
	RepeaterID RepeaterID
}

// Kind implements Message.
func (Ping) Kind() Kind { return KindPing }

// Marshal implements Message.
func (m Ping) Marshal() []byte { return m.AppendTo(make([]byte, 0, pingSize)) }

// AppendTo implements Message.
func (m Ping) AppendTo(dst []byte) []byte {
	dst = append(dst, "RPTPING"...)
	var id [4]byte
	putID(id[:], m.RepeaterID)
	return append(dst, id[:]...)
}

func parsePing(b []byte) (Message, error) {
	if err := exactLen(b, pingSize, KindPing); err != nil {
		return nil, err
	}
	return Ping{RepeaterID: getID(b[7:11])}, nil
}

// Pong is a master's keepalive reply. Tag "MSTPONG".
type Pong struct {
	RepeaterID RepeaterID
}

// Kind implements Message.
func (Pong) Kind() Kind { return KindPong }

// Marshal implements Message.
func (m Pong) Marshal() []byte { return m.AppendTo(make([]byte, 0, pongSize)) }

// AppendTo implements Message.
func (m Pong) AppendTo(dst []byte) []byte {
	dst = append(dst, "MSTPONG"...)
	var id [4]byte
	putID(id[:], m.RepeaterID)
	return append(dst, id[:]...)
}

func parsePong(b []byte) (Message, error) {
	if err := exactLen(b, pongSize, KindPong); err != nil {
		return nil, err
	}
	return Pong{RepeaterID: getID(b[7:11])}, nil
}

// GatewayPong is the keepalive reply used on a local gateway link. Tag "DMRP".
//
// It is four bytes and carries no repeater ID, unlike the master link's
// eleven-byte MSTPONG. The difference is real and appears in
// testdata/hbp/hbp-login-session.pcap, where both dialects run concurrently on
// one host.
type GatewayPong struct{}

// Kind implements Message.
func (GatewayPong) Kind() Kind { return KindGatewayPong }

// Marshal implements Message.
func (m GatewayPong) Marshal() []byte { return m.AppendTo(make([]byte, 0, gatewayPongSize)) }

// AppendTo implements Message.
func (m GatewayPong) AppendTo(dst []byte) []byte { return append(dst, "DMRP"...) }

func parseGatewayPong(b []byte) (Message, error) {
	if err := exactLen(b, gatewayPongSize, KindGatewayPong); err != nil {
		return nil, fmt.Errorf("%w (DMRP carries no repeater ID, unlike MSTPONG)", err)
	}
	return GatewayPong{}, nil
}
