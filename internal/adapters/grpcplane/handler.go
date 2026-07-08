package grpcplane

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// mockMatcher is the subset of *usecase.MatchRequest the handler needs, named
// at the point of use per Go convention (same shape proxy.Handler depends on).
type mockMatcher interface {
	Execute(ctx context.Context, partition string, in usecase.MatchInput) (domain.Mock, bool, error)
}

// trafficRecorder is the subset of *usecase.RecordTraffic the handler needs.
type trafficRecorder interface {
	Execute(ctx context.Context, in usecase.RecordTrafficInput) (domain.TrafficRecord, error)
}

// handler serves every unary gRPC method generically. It is registered as the
// server's UnknownServiceHandler, so it sees all calls regardless of service.
type handler struct {
	match        mockMatcher
	record       trafficRecorder
	defaultSpace string
	bodyCap      int64
	clock        usecase.Clock
	log          *slog.Logger
}

// handle is the grpc.StreamHandler installed via grpc.UnknownServiceHandler.
// It reads a single (unary) request message, runs the existing match→respond
// decision, writes the response, records traffic, and never panics: a
// recovered panic or any error becomes a clean gRPC status.
func (h *handler) handle(_ any, stream grpc.ServerStream) (err error) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("grpcplane: recovered panic", "panic", r)
			err = status.Error(codes.Internal, "internal error")
		}
	}()

	ctx := stream.Context()
	fullMethod, ok := grpc.MethodFromServerStream(stream)
	if !ok {
		return status.Error(codes.Internal, "grpcplane: could not determine method")
	}

	start := h.clock.Now()

	var reqBytes []byte
	if err := stream.RecvMsg(&reqBytes); err != nil {
		// Client hung up or sent nothing decodable at the transport level.
		return err
	}

	reqHeaders := metadataToHeaders(ctx)

	fields, decErr := decodeFields(reqBytes)
	if decErr != nil {
		h.recordCall(ctx, fullMethod, start, reqHeaders, reqBytes, domain.DecisionInternalError, nil, nil)
		return status.Errorf(codes.InvalidArgument, "grpcplane: malformed request message: %v", decErr)
	}

	projected, projErr := projectForMatch(fields)
	if projErr != nil {
		h.recordCall(ctx, fullMethod, start, reqHeaders, reqBytes, domain.DecisionInternalError, nil, nil)
		return status.Errorf(codes.InvalidArgument, "grpcplane: could not project request: %v", projErr)
	}

	in := usecase.MatchInput{
		Method: "POST", // gRPC is always HTTP/2 POST
		Path:   fullMethod,
		Header: reqHeaders,
		Body:   projected,
	}

	mock, matched, matchErr := h.match.Execute(ctx, h.defaultSpace, in)
	if matchErr != nil {
		h.recordCall(ctx, fullMethod, start, reqHeaders, reqBytes, domain.DecisionInternalError, nil, nil)
		return status.Errorf(codes.Internal, "grpcplane: matching failed: %v", matchErr)
	}

	if !matched || mock.Action.Kind != domain.ActionRespond || mock.Action.Respond == nil {
		// No responder matched. gRPC has no spy-passthrough analogue (no real
		// upstream to forward an arbitrary method to), so this is a clean
		// Unimplemented — the documented gRPC counterpart to the HTTP plane's
		// "nothing configured" (proxy/fault mocks are not served here).
		h.recordCall(ctx, fullMethod, start, reqHeaders, reqBytes, domain.DecisionNotConfigured, nil, nil)
		return status.Errorf(codes.Unimplemented, "grpcplane: no gRPC mock matched %s", fullMethod)
	}

	respBytes, buildErr := buildResponse(mock.Action.Respond.Body, fields)
	if buildErr != nil {
		mockID := mock.ID
		h.recordCall(ctx, fullMethod, start, reqHeaders, reqBytes, domain.DecisionInternalError, &mockID, nil)
		return status.Errorf(codes.Internal, "grpcplane: building response for mock %q: %v", mock.ID, buildErr)
	}

	if err := stream.SendMsg(&respBytes); err != nil {
		return err
	}

	mockID := mock.ID
	h.recordCall(ctx, fullMethod, start, reqHeaders, reqBytes, domain.DecisionMocked, &mockID, respBytes)
	return nil
}

// recordCall writes one traffic record, mirroring proxy.Handler's discipline:
// a recording failure must never fail an already-served RPC (Principle III —
// losing traffic-log data is acceptable), so it is only logged.
func (h *handler) recordCall(
	ctx context.Context, method string, start time.Time,
	reqHeaders map[string][]string, reqBody []byte,
	decision domain.Decision, mockID *string, respBody []byte,
) {
	reqStored, reqTrunc, reqTotal := capBody(reqBody, h.bodyCap)
	respStored, respTrunc, respTotal := capBody(respBody, h.bodyCap)
	_, err := h.record.Execute(ctx, usecase.RecordTrafficInput{
		Partition: h.defaultSpace, Method: "POST", Path: method,
		RequestHeaders: reqHeaders, RequestBody: reqStored,
		RequestBodyTruncated: reqTrunc, RequestBodyTotalSize: reqTotal,
		Decision: decision, MatchedMockID: mockID,
		ResponseBody: respStored, ResponseBodyTruncated: respTrunc, ResponseBodyTotalSize: respTotal,
		Status:    0, // gRPC has no HTTP status; the decision carries the outcome
		LatencyMS: int(h.clock.Now().Sub(start).Milliseconds()),
	})
	if err != nil {
		h.log.Warn("grpcplane: record traffic failed", "err", err)
	}
}

// metadataToHeaders projects incoming gRPC metadata into the header shape the
// matcher/recorder expect. Keys are already lowercased by gRPC.
func metadataToHeaders(ctx context.Context) map[string][]string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(md))
	for k, v := range md {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// capBody bounds a stored body to limit bytes, reporting truncation and the
// true size — same contract as the HTTP recorder's capped capture.
func capBody(b []byte, limit int64) (stored []byte, truncated bool, total int64) {
	total = int64(len(b))
	if limit > 0 && total > limit {
		return b[:limit], true, total
	}
	return b, false, total
}
