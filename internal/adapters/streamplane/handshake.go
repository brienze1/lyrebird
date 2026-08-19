package streamplane

import (
	"fmt"
	"strings"
)

// handshakeProtocol is the token opening every handshake line. It carries a
// version so a future framing of the handshake itself can be introduced
// without guessing at what an old stand-in meant.
const handshakeProtocol = "LYREBIRD/1"

// handshakeSpaceKey is the reserved handshake key that selects a space. Every
// other key becomes a match header, so a rule can condition on whatever a
// stand-in chose to announce about itself.
const handshakeSpaceKey = "space"

// The exact one-line replies of contracts/stream-data-plane.md. They are
// constants rather than inline strings because a stand-in parses them, so
// they are protocol, not log text.
const (
	replyOK               = "OK"
	replyMalformed        = "ERR malformed handshake"
	replyHandshakeTimeout = "ERR handshake timeout"
)

func replyUnknownEndpoint(name string) string {
	return "ERR unknown endpoint " + name
}

func replyOccupied(name string) string {
	return "ERR endpoint " + name + " already connected"
}

// handshake is a parsed opening line.
type handshake struct {
	// Endpoint is the boundary the stand-in claims to be standing in for.
	Endpoint string
	// Space is the partition it belongs to, empty when the stand-in did not
	// name one (the default space then applies). This is the space selection
	// the gRPC plane could not have, since a gRPC client cannot carry
	// Lyrebird's space header.
	Space string
	// Header carries every other key=value pair, projected into
	// MatchInput.Header so a rule can match on it.
	Header map[string][]string
}

// parseHandshake reads the opening line "LYREBIRD/1 <endpoint> [k=v ...]".
//
// It is strict about the protocol token and the endpoint, and permissive
// about everything after: an unrecognised key is carried into Header rather
// than refused, so a stand-in announcing something Lyrebird has no opinion
// about is not blocked from connecting by a version skew.
func parseHandshake(line string) (handshake, error) {
	fields := strings.Fields(strings.TrimRight(line, "\r\n"))
	if len(fields) < 2 || fields[0] != handshakeProtocol {
		return handshake{}, fmt.Errorf("streamplane: handshake must be %q followed by an endpoint name", handshakeProtocol)
	}

	h := handshake{Endpoint: fields[1]}
	if strings.Contains(h.Endpoint, "=") {
		// A "space=x" in the endpoint position means the stand-in forgot to
		// name an endpoint at all; saying so beats treating "space=x" as an
		// endpoint name and refusing it as unknown three lines later.
		return handshake{}, fmt.Errorf("streamplane: handshake names no endpoint (got the key %q first)", h.Endpoint)
	}

	for _, f := range fields[2:] {
		key, value, ok := strings.Cut(f, "=")
		if !ok || key == "" {
			return handshake{}, fmt.Errorf("streamplane: handshake option %q is not key=value", f)
		}
		if strings.EqualFold(key, handshakeSpaceKey) {
			h.Space = value
			continue
		}
		if h.Header == nil {
			h.Header = map[string][]string{}
		}
		// Lowercased so a header condition behaves the same here as on the
		// HTTP and gRPC planes, where the transport has already normalised
		// the case for the matcher.
		lower := strings.ToLower(key)
		h.Header[lower] = append(h.Header[lower], value)
	}
	return h, nil
}
