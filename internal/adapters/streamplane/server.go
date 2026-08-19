package streamplane

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// handshakeDeadline bounds how long a connection may sit having said
// nothing. Without it a stand-in that opens a socket and dies holds an
// accept slot forever, and — worse — could hold an endpoint nobody can
// reclaim if it had got as far as claiming one.
const handshakeDeadline = 10 * time.Second

// endpointResolver is the subset of *usecase.Endpoints the plane needs at
// connect time: resolve a handshake's name to a declared endpoint, across
// both ephemeral and seeded sources.
type endpointResolver interface {
	Get(ctx context.Context, partition, name string) (domain.Endpoint, error)
}

// Deps are the collaborators the byte-stream data plane needs — all existing
// use cases and infra, so this adapter adds transport only, no new business
// logic.
type Deps struct {
	Match        mockMatcher
	Record       trafficRecorder
	Endpoints    endpointResolver
	Registry     *Registry
	Templater    usecase.Templater
	Script       usecase.RespondScriptEval
	DefaultSpace string
	BodyCapBytes int64
	Clock        usecase.Clock
	Log          *slog.Logger
}

// Server is the byte-stream data-plane listener. It accepts TCP connections,
// completes the handshake, and hands each accepted connection to a conn that
// owns it until the stand-in goes away.
type Server struct {
	deps     Deps
	registry *Registry
	log      *slog.Logger

	handler *Handler

	mu       sync.Mutex
	ln       net.Listener
	closed   bool
	cancel   context.CancelFunc
	inflight sync.WaitGroup
}

// New builds the byte-stream data-plane server. It does not bind a listener;
// call Serve with one from the bootstrap layer, exactly as grpcplane.Server
// does.
func New(d Deps) *Server {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	registry := d.Registry
	if registry == nil {
		registry = NewRegistry()
	}
	return &Server{
		deps:     d,
		registry: registry,
		log:      log,
		handler: &Handler{
			match:   d.Match,
			record:  d.Record,
			tpl:     d.Templater,
			script:  d.Script,
			bodyCap: d.BodyCapBytes,
			clock:   d.Clock,
			log:     log,
		},
	}
}

// Registry exposes the live connection registry so bootstrap can hand the
// same instance to the use cases that need it (emit_frame, reset).
func (s *Server) Registry() *Registry { return s.registry }

// Serve blocks accepting connections on ln until Shutdown is called. It
// returns nil on a clean shutdown, mirroring grpc.Server.Serve's contract so
// bootstrap treats both listeners the same way.
func (s *Server) Serve(ln net.Listener) error {
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return nil
	}
	s.ln = ln
	s.cancel = cancel
	s.mu.Unlock()

	defer cancel()
	for {
		nc, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if !s.track() {
			// Shutdown has already begun; refusing here rather than
			// spawning is what keeps inflight.Add out of a race with
			// inflight.Wait.
			_ = nc.Close()
			continue
		}
		go func() {
			defer s.inflight.Done()
			s.accept(ctx, nc)
		}()
	}
}

// track registers one in-flight connection, reporting false once Shutdown
// has begun.
//
// The registration happens under the SAME mutex that guards closed, and
// Shutdown sets closed under that mutex before it waits. Without that
// pairing, sync.WaitGroup.Add in the accept loop can run concurrently with
// Wait in Shutdown while the counter is zero — which the race detector flags,
// and which the WaitGroup documentation explicitly forbids.
func (s *Server) track() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.inflight.Add(1)
	return true
}

// Shutdown stops accepting, drops every live connection (which stops their
// cadences), and waits for the in-flight handlers to finish. It is the
// byte-stream peer of grpc.Server.GracefulStop, and like it has no deadline
// of its own: bootstrap owns the drain budget.
func (s *Server) Shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	ln, cancel := s.ln, s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if ln != nil {
		_ = ln.Close()
	}
	s.registry.closeAll()
	s.inflight.Wait()
}

// accept completes the handshake and, if it succeeds, serves the connection.
// Every refusal writes its one-line reason before closing, so a stand-in
// learns at bring-up why it is not connected rather than sitting in silence
// (FR-003, FR-030).
func (s *Server) accept(ctx context.Context, nc net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("streamplane: recovered panic accepting connection", "panic", r)
			_ = nc.Close()
		}
	}()

	if err := nc.SetReadDeadline(time.Now().Add(handshakeDeadline)); err != nil {
		s.log.Debug("streamplane: could not set handshake deadline", "err", err)
	}
	line, err := bufio.NewReader(nc).ReadString('\n')
	if err != nil {
		s.refuse(nc, replyHandshakeTimeout)
		return
	}
	// Clear the deadline: it exists to bound the handshake, not to impose a
	// read timeout on a stand-in that may legitimately stay silent for
	// minutes (a source that only listens, for instance).
	if err := nc.SetReadDeadline(time.Time{}); err != nil {
		s.log.Debug("streamplane: could not clear handshake deadline", "err", err)
	}

	hs, err := parseHandshake(line)
	if err != nil {
		s.refuse(nc, replyMalformed)
		return
	}

	partition := hs.Space
	if partition == "" {
		partition = s.deps.DefaultSpace
	}
	if partition == "" {
		partition = domain.DefaultPartitionID
	}

	endpoint, err := s.deps.Endpoints.Get(ctx, partition, hs.Endpoint)
	if err != nil {
		// Refused rather than accepted-and-silent, so a typo or a renamed
		// endpoint surfaces here instead of as silence three scenarios later
		// (FR-003). An endpoint is never brought into existence implicitly.
		s.refuse(nc, replyUnknownEndpoint(hs.Endpoint))
		return
	}

	c := newConn(nc, partition, endpoint, hs, s.handler, s.registry, s.log)
	if err := s.registry.claim(partition, endpoint.Name, c); err != nil {
		s.refuse(nc, replyOccupied(endpoint.Name))
		return
	}

	if _, err := nc.Write([]byte(replyOK + "\r\n")); err != nil {
		c.close()
		return
	}
	c.serve(ctx, s.deps.BodyCapBytes)
}

// refuse writes one explanatory line and closes. The write is best-effort: a
// stand-in that has already gone away cannot be told anything.
func (s *Server) refuse(nc net.Conn, reply string) {
	_, _ = nc.Write([]byte(reply + "\r\n"))
	_ = nc.Close()
}
