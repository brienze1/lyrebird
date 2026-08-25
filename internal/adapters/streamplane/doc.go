// Package streamplane is Lyrebird's generic byte-stream data plane.
//
// It is a peer of internal/adapters/proxy (HTTP) and internal/adapters/
// grpcplane (gRPC): an inward-depending adapter that turns an inbound frame
// into the SAME match→respond decision those planes use, consuming the
// existing usecase.MatchRequest and usecase.RecordTraffic use cases. It adds
// a transport, not a control model — mocks, spaces, seed config, reset, TTL
// and GC are all reused unchanged.
//
// A stand-in process dials TCP, names an endpoint (and optionally a space) in
// a one-line handshake, and from then on every frame in either direction is
// framed, projected, matched, answered and recorded.
//
// # Nothing protocol-specific may ever live here
//
// This is constitution Principle I, and it is the reason this package exists
// in the shape it does. Where a frame ends is a declarative Framing; how its
// bytes decompose into addressable fields is a declarative Projection; how an
// answer is assembled is a declarative list of FrameParts. There is no
// decoder, parser or special case for any real protocol in this package, and
// adding one would be a constitution violation to be redesigned as
// configuration — exactly as grpcplane parses protobuf at the wire level and
// leaves every SDK's behaviour to recipes.
//
// # What this plane adds that the others lack
//
//   - Space selection at connect time. A gRPC client cannot carry Lyrebird's
//     space header, so grpcplane serves the default space only; a stream
//     handshake is Lyrebird's own line, so "space=" fits naturally.
//   - Scripted responses. grpcplane bypasses
//     usecase.BuildRespondOutputWithScript; this plane routes through it, so
//     a mock's Script.RespondSrc builds the frame.
//   - Origination. Bytes can be pushed to a connected stand-in with no
//     inbound frame to provoke them, driven by a declared cadence on the
//     endpoint or by an injection over the control plane.
//
// # What it deliberately does not do
//
// domain.ActionProxy is not served. The other planes forward to a real
// upstream; here there is none by design — a stand-in exists precisely so
// that no device is attached. Such a rule is rejected when it is created
// (usecase.Endpoints), so the limitation surfaces where the author can act on
// it rather than as a mysteriously silent frame at serve time.
package streamplane
