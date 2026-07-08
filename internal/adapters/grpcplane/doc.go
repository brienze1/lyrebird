// Package grpcplane is Lyrebird's generic plaintext-gRPC (h2c) data plane.
//
// It is a peer of internal/adapters/proxy (the HTTP data plane): an
// inward-depending adapter that turns an incoming unary gRPC call into the
// SAME match→respond decision the HTTP plane uses, consuming the existing
// usecase.MatchRequest and usecase.RecordTraffic use cases. It adds a
// transport, not a control model — mocks, spaces, seed config, reset, and GC
// are all reused unchanged.
//
// Everything gRPC-specific lives here and NOTHING here is service-specific
// (constitution Principle I). A gRPC server built with
// grpc.UnknownServiceHandler + a raw []byte codec accepts every method
// without any compiled protobuf schema. The request message is parsed at the
// protobuf WIRE level (field number → value) and projected into
// usecase.MatchInput.Body so the existing matchers apply; a matched mock's
// respond body is a declarative field-spec (see protowire.go) re-encoded to
// the wire. Support for a specific SDK (GCP KMS, Pub/Sub, …) is therefore
// delivered entirely as recipes/seed config — never as a branch in this code.
package grpcplane
