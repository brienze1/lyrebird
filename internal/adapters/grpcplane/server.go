package grpcplane

import (
	"log/slog"
	"net"

	"google.golang.org/grpc"

	"github.com/brienze1/lyrebird/internal/usecase"
)

// Deps are the collaborators the gRPC data plane needs — all existing use
// cases and infra, so this adapter adds transport only, no new business logic.
type Deps struct {
	Match        mockMatcher
	Record       trafficRecorder
	DefaultSpace string
	BodyCapBytes int64
	Clock        usecase.Clock
	Log          *slog.Logger
}

// Server is the plaintext-gRPC (h2c) data-plane listener. It wraps a
// grpc.Server configured with a raw passthrough codec and a single
// UnknownServiceHandler, so it serves every unary method generically.
type Server struct {
	grpc *grpc.Server
	log  *slog.Logger
}

// New builds the gRPC data-plane server. It does not bind a listener; call
// Serve with one from the bootstrap layer.
func New(d Deps) *Server {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	h := &handler{
		match:        d.Match,
		record:       d.Record,
		defaultSpace: d.DefaultSpace,
		bodyCap:      d.BodyCapBytes,
		clock:        d.Clock,
		log:          log,
	}
	srv := grpc.NewServer(
		grpc.ForceServerCodec(rawCodec{}),
		grpc.UnknownServiceHandler(h.handle),
	)
	return &Server{grpc: srv, log: log}
}

// Serve blocks serving the gRPC data plane on ln until GracefulStop is called
// or a fatal error occurs. grpc.Server.Serve returns nil on GracefulStop.
func (s *Server) Serve(ln net.Listener) error {
	return s.grpc.Serve(ln)
}

// GracefulStop stops accepting new calls and waits for in-flight ones to
// finish, then unblocks Serve.
func (s *Server) GracefulStop() {
	s.grpc.GracefulStop()
}
