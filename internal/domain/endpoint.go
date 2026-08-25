package domain

import "time"

// FramingKind selects which of Framing's variants is active.
type FramingKind string

// The FramingKind values a Framing may have. Three variants cover framed
// protocols generically; none of them implies a specific protocol, which is
// the whole point (constitution Principle I).
const (
	// FramingDelimiter ends a frame at the first occurrence of Delimiter.
	// The delimiter is part of the frame as recorded, and is appended to a
	// built answer unless that answer declares itself raw.
	FramingDelimiter FramingKind = "delimiter"
	// FramingLength makes every frame exactly Length bytes.
	FramingLength FramingKind = "length"
	// FramingPrefix reads PrefixWidth bytes carrying the length of what
	// follows, in PrefixEndian byte order.
	FramingPrefix FramingKind = "prefix"
)

// Endianness selects the byte order of a length prefix.
type Endianness string

// The Endianness values a prefix framing may declare. Big-endian is the
// default because network byte order is what length prefixes conventionally
// use; little-endian is available for protocols that do not follow it.
const (
	EndianBig    Endianness = "big"
	EndianLittle Endianness = "little"
)

// Framing declares where one frame ends and the next begins. Exactly one
// variant is populated, selected by Kind. It belongs to an Endpoint and is
// never overridable by a rule (FR-005): a real wire has one framing, and two
// rules disagreeing about where a frame ends would make the same bytes mean
// two different things.
type Framing struct {
	Kind         FramingKind
	Delimiter    []byte
	Length       int
	PrefixWidth  int
	PrefixEndian Endianness
}

// ProjectionAs selects how a run of bytes is rendered into the frame
// envelope's $.at map.
type ProjectionAs string

// The ProjectionAs values a ProjectionField may declare. An empty value means
// ProjectAsText, so the common case needs no annotation.
const (
	// ProjectAsText renders the bytes latin-1, so every byte round-trips.
	ProjectAsText ProjectionAs = "text"
	// ProjectAsInt renders the bytes as their decimal text parsed to a
	// number. Bytes that are not a decimal number yield an absent value.
	ProjectAsInt ProjectionAs = "int"
	// ProjectAsHex renders the bytes as lowercase hex.
	ProjectAsHex ProjectionAs = "hex"
)

// ProjectionField is one named run of bytes taken at a position, populating
// $.at.<Name> in the frame envelope (data-model.md §4). A field addressing
// bytes the frame does not have yields an ABSENT value rather than an error,
// so an `exists: false` condition can match on it (FR-027).
type ProjectionField struct {
	Name   string
	Offset int
	Length int
	As     ProjectionAs
}

// Projection declares how a frame's bytes decompose into addressable fields.
// It is declared on an Endpoint as the default for every rule on it, and
// overridable by an individual Mock (FR-006). A rule that declares one
// replaces the endpoint's entirely for that rule; a rule that declares none
// inherits the endpoint's.
//
// Nothing here names a protocol: Split and Offset/Length are the only
// vocabulary, so supporting a peripheral Lyrebird has never seen is
// configuration, never code (FR-008).
type Projection struct {
	// Split, when non-empty, populates $.fields by splitting the frame on
	// this separator.
	Split string
	// At, when non-empty, populates $.at with one entry per field.
	At []ProjectionField
}

// OnExhaustion selects what a Cadence does once its declared sequence runs
// out. It is deliberately a separate vocabulary from Scenario's OnExhaust:
// a cadence repeats over time with no request driving it, so "fallthrough"
// (Scenario's option of letting a lower-priority mock answer instead) has no
// meaning here — there is no request to fall through to another rule.
type OnExhaustion string

// The OnExhaustion values a Cadence may declare.
const (
	// OnExhaustionRepeatLast keeps emitting the final frame. The default,
	// because it is what a stationary source does (FR-011).
	OnExhaustionRepeatLast OnExhaustion = "repeat_last"
	// OnExhaustionLoop restarts the sequence from the beginning.
	OnExhaustionLoop OnExhaustion = "loop"
	// OnExhaustionStop falls silent.
	OnExhaustionStop OnExhaustion = "stop"
)

// Cadence declares unprompted emission: a sequence of frames pushed to a
// connected stand-in, with nothing having arrived to provoke them (FR-011).
// It belongs to an Endpoint, starts when a stand-in occupies it, and stops
// when the connection ends or the space is reset.
//
// Interval > 0 ticks on Lyrebird's own host clock — a real-time heartbeat.
// Interval == 0 is "immediate": every frame is queued back-to-back with no
// wait, paced only by the connection's own backpressure, never by a clock —
// for a source whose bytes are simply there for whoever reads them, where
// nothing may depend on how much real time has passed since occupancy
// (streamplane.conn.runCadence). Interval must never be negative.
type Cadence struct {
	Interval  time.Duration
	Frames    [][]FramePart
	OnExhaust OnExhaustion
}

// FramePart is one element of the declarative grammar that builds an outbound
// frame (data-model.md §5). Exactly one of Text/Hex/Int/CopyFrom/Repeat is
// set; the parts of a spec are concatenated in order.
//
// This generalises the gRPC plane's response descriptor (string/bytes/int/
// copyFrom/raw, see adapters/grpcplane/protowire.go) from protobuf fields to
// raw bytes, so an answer that echoes part of a request needs no code
// (FR-009).
type FramePart struct {
	Text     *string
	Hex      *string
	Int      *int64
	CopyFrom *string // a JSONPath into the triggering frame's envelope
	// Width/Pad apply to Int only: the number is rendered as decimal text and
	// left-padded with Pad (default "0") to Width runes. Width <= 0 means no
	// padding.
	Width int
	Pad   string
	// Repeat, when non-nil, emits Times copies of that part. Times <= 0
	// emits nothing, which is a valid (if pointless) spec rather than an
	// error.
	Repeat *FramePart
	Times  int
}

// FrameSpec is a complete outbound frame declaration: the ordered parts, plus
// whether the endpoint's framing terminator is suppressed.
type FrameSpec struct {
	Parts []FramePart
	// Raw suppresses the automatic framing terminator. This is how a
	// `malformed` fault produces a frame whose terminator is missing, so the
	// consumer's own validation is what rejects it (FR-016).
	Raw bool
}

// Endpoint is a named byte-stream boundary a stand-in connects to. Rules,
// captures and cadence belong to it, and it lives inside a space
// (data-model.md §1).
//
// Occupancy is deliberately NOT a field: it is runtime state owned by the
// connection registry, not something persisted. At most one connection may
// hold an endpoint at a time within a space (FR-030).
type Endpoint struct {
	Name       string
	Partition  string
	Framing    Framing
	Projection *Projection
	Cadence    *Cadence
	// Lifetime reuses domain.Mock's own vocabulary, so a seeded endpoint
	// survives reset and never expires and an ephemeral one does neither —
	// exactly the semantics seeded mocks already have (FR-022).
	Lifetime  Lifetime
	CreatedAt time.Time
}

// StreamMethod is the MatchInput.Method every byte-stream frame carries. It
// is a constant rather than a per-frame value because a byte stream has no
// method vocabulary of its own — the direction lives in the traffic record,
// not in what a rule matches on. A rule may therefore omit method entirely
// and match on path alone, exactly as gRPC mocks do.
const StreamMethod = "FRAME"

// StreamHost is the MatchInput.Host (and traffic record host) every
// byte-stream frame carries. A stream connection has no authority in the HTTP
// sense; a fixed value keeps `host`-filtered traffic queries meaningful and
// lets a rule scope itself to this plane with a header-free condition.
const StreamHost = "stream"

// Stream traffic directions, recorded in the traffic record's method column
// so that an answer, an inbound frame and an unprompted emission stay
// distinguishable (FR-013, FR-018, data-model.md §7).
const (
	// StreamDirectionIn is a frame from the stand-in to Lyrebird.
	StreamDirectionIn = "IN"
	// StreamDirectionOut is an answer to an inbound frame.
	StreamDirectionOut = "OUT"
	// StreamDirectionEmit is an unprompted emission — a cadence tick or an
	// injection — which nothing asked for.
	StreamDirectionEmit = "EMIT"
)
