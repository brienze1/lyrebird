package grpcplane

import "fmt"

// rawCodec is a passthrough gRPC codec: it moves the raw message bytes
// verbatim between the wire and a *[]byte, doing no protobuf (de)serialization
// of its own. Combined with grpc.UnknownServiceHandler this lets the server
// accept every method without any registered service or compiled schema —
// the handler then parses/builds the protobuf at the wire level itself.
//
// Name is "proto" so the server's response content-type is the
// application/grpc+proto that GCP (and other) protobuf clients expect; when a
// codec is forced via grpc.ForceServerCodec the server uses it for every call
// regardless of the request's content-subtype, so the name does not gate
// which calls are accepted.
type rawCodec struct{}

// Marshal returns the bytes to put on the wire. The handler always sends a
// *[]byte (the already-encoded response message).
func (rawCodec) Marshal(v any) ([]byte, error) {
	b, ok := v.(*[]byte)
	if !ok {
		return nil, fmt.Errorf("grpcplane: raw codec Marshal expected *[]byte, got %T", v)
	}
	return *b, nil
}

// Unmarshal copies the wire bytes into the handler's *[]byte. The copy is
// deliberate: gRPC may reuse or free the backing buffer once Unmarshal
// returns, and the handler holds onto these bytes past that point (to record
// them as request traffic and to echo request fields into the response).
func (rawCodec) Unmarshal(data []byte, v any) error {
	b, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("grpcplane: raw codec Unmarshal expected *[]byte, got %T", v)
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	*b = cp
	return nil
}

func (rawCodec) Name() string { return "proto" }
