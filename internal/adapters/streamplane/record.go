package streamplane

import (
	"context"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// Recording maps a frame onto the EXISTING traffic table with no migration
// (data-model.md §7): the direction goes in `method`, a fixed "stream" in
// `host`, the endpoint in `path`, and `status` is 0 — the gRPC plane already
// writes 0 there for the same reason, since neither plane has an HTTP status.
//
// That is what makes list_traffic, get_traffic, promote_traffic,
// clear_traffic and metrics work on this plane with no adapter change at all
// (FR-018, FR-019, FR-020).

// recordInbound writes the IN record for a frame that arrived.
//
// It carries no latency: an inbound frame waited for nothing. Latency is
// measured on the OUT record, from when its triggering frame arrived, which is
// where a delay fault's declared delay becomes visible.
func (h *Handler) recordInbound(
	ctx context.Context, c *conn, frame []byte, decision domain.Decision, mockID *string,
) {
	stored, truncated, total := capBody(frame, h.bodyCap)
	h.write(ctx, usecase.RecordTrafficInput{
		Partition: c.partition,
		Method:    domain.StreamDirectionIn,
		Host:      domain.StreamHost,
		Path:      "/" + c.endpoint.Name,

		RequestHeaders:       c.header,
		RequestBody:          stored,
		RequestBodyTruncated: truncated,
		RequestBodyTotalSize: total,

		Decision:      decision,
		MatchedMockID: mockID,
	})
}

// recordOutbound writes the record for a frame that left. It runs inside the
// connection's writer goroutine, so the traffic log's order is the wire's
// order rather than the order handlers happened to finish in.
func (h *Handler) recordOutbound(ctx context.Context, c *conn, o outbound) {
	reqStored, reqTruncated, reqTotal := capBody(o.requestFrame, h.bodyCap)
	respStored, respTruncated, respTotal := capBody(o.bytes, h.bodyCap)

	// Latency is measured only for an answer, and from when its triggering
	// frame arrived — so a delay fault's declared delay is visible in the
	// record, which is what FR-016's "the delay is visible in what was
	// recorded" asks for. An unprompted EMIT answered nothing, so it has no
	// meaningful latency.
	latencyMS := 0
	if o.direction == domain.StreamDirectionOut && !o.startedAt.IsZero() {
		latencyMS = int(h.clock.Now().Sub(o.startedAt).Milliseconds())
	}

	h.write(ctx, usecase.RecordTrafficInput{
		Partition: c.partition,
		Method:    o.direction,
		Host:      domain.StreamHost,
		Path:      "/" + c.endpoint.Name,

		RequestHeaders:       c.header,
		RequestBody:          reqStored,
		RequestBodyTruncated: reqTruncated,
		RequestBodyTotalSize: reqTotal,

		Decision:      o.decision,
		MatchedMockID: o.mockID,

		ResponseBody:          respStored,
		ResponseBodyTruncated: respTruncated,
		ResponseBodyTotalSize: respTotal,

		LatencyMS: latencyMS,
	})
}

// recordOversized records the one thing that has no frame to show for it: a
// run of bytes that never completed a frame and was abandoned at the cap
// (FR-034). Without this record, a stand-in writing malformed bytes forever
// would look exactly like one writing nothing at all.
func (h *Handler) recordOversized(ctx context.Context, c *conn) {
	h.write(ctx, usecase.RecordTrafficInput{
		Partition: c.partition,
		Method:    domain.StreamDirectionIn,
		Host:      domain.StreamHost,
		Path:      "/" + c.endpoint.Name,

		RequestHeaders:       c.header,
		RequestBody:          []byte(ErrFrameTooLarge.Error()),
		RequestBodyTruncated: true,

		Decision: domain.DecisionInternalError,
	})
}

// write performs the record. A recording failure is logged, never returned:
// losing traffic-log data is acceptable (constitution Principle III), while
// failing a frame that has already been served is not — the same discipline
// proxy.Handler and grpcplane.handler apply.
func (h *Handler) write(ctx context.Context, in usecase.RecordTrafficInput) {
	if _, err := h.record.Execute(ctx, in); err != nil {
		h.log.Warn("streamplane: record traffic failed", "path", in.Path, "err", err)
	}
}

// capBody bounds a stored body to the configured cap, reporting truncation
// and the true size — so an oversized frame is still SERVED in full while its
// record says plainly that what was stored is not (FR-028). Identical
// contract to grpcplane's capBody and the HTTP recorder's capped capture.
func capBody(b []byte, limit int64) (stored []byte, truncated bool, total int64) {
	total = int64(len(b))
	if limit > 0 && total > limit {
		return b[:limit], true, total
	}
	return b, false, total
}
