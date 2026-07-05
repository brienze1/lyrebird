package mcp

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// ListTrafficIn is list_traffic's input. Field-for-field parity with
// httpadmin's parseTrafficFilter (method/host/path_prefix/status/since/
// until/limit) is deliberate — REST must not expose a filter MCP lacks
// (constitution Principle II).
type ListTrafficIn struct {
	Space  string `json:"space,omitempty" jsonschema:"space/partition to list; defaults to the server's default space"`
	Method string `json:"method,omitempty" jsonschema:"filter to this exact HTTP method"`
	Host   string `json:"host,omitempty" jsonschema:"filter to this exact host"`
	Path   string `json:"path,omitempty" jsonschema:"filter to paths starting with this prefix"`
	Status *int   `json:"status,omitempty" jsonschema:"filter to this exact HTTP status"`
	Since  string `json:"since,omitempty" jsonschema:"RFC3339 timestamp; only traffic at or after this time"`
	Until  string `json:"until,omitempty" jsonschema:"RFC3339 timestamp; only traffic at or before this time"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of records to return"`
}

// ListTrafficOut is list_traffic's and inspect_requests's output.
type ListTrafficOut struct {
	Traffic []dto.TrafficSummaryDTO `json:"traffic"`
}

// GetTrafficIn is get_traffic's input.
type GetTrafficIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition the traffic was recorded in; defaults to the server's default space"`
	ID    string `json:"id" jsonschema:"traffic record id, as returned by list_traffic/inspect_requests"`
}

// InspectRequestsIn is inspect_requests's input.
type InspectRequestsIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition to inspect; defaults to the server's default space"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of recent requests to return (default 20)"`
}

// ClearTrafficIn is clear_traffic's input.
type ClearTrafficIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition to clear; defaults to the server's default space"`
}

// ClearTrafficOut is clear_traffic's output.
type ClearTrafficOut struct {
	Cleared bool `json:"cleared"`
}

// MetricsIn is metrics's input.
type MetricsIn struct {
	Space  string `json:"space,omitempty" jsonschema:"space/partition to aggregate; defaults to the server's default space"`
	Window string `json:"window,omitempty" jsonschema:"duration string like \"1h\" or \"30m\"; omitted means all retained traffic"`
}

// MetricBucketDTO is the wire shape of one usecase.MetricBucket.
type MetricBucketDTO struct {
	MockID       string  `json:"mock_id,omitempty"`
	Path         string  `json:"path"`
	Status       int     `json:"status"`
	Count        int     `json:"count"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
	P95LatencyMS float64 `json:"p95_latency_ms"`
}

// MetricsOut is metrics's output.
type MetricsOut struct {
	Window  string            `json:"window,omitempty"`
	Total   int               `json:"total"`
	Buckets []MetricBucketDTO `json:"buckets"`
}

// PromoteTrafficIn is promote_traffic's input.
type PromoteTrafficIn struct {
	Space      string `json:"space,omitempty" jsonschema:"space/partition the traffic was recorded in; defaults to the server's default space"`
	TrafficID  string `json:"traffic_id" jsonschema:"id of the recorded interaction to promote, from list_traffic/get_traffic"`
	Name       string `json:"name,omitempty" jsonschema:"name for the new mock; defaults to \"promoted-\"+traffic_id"`
	TTLSeconds *int   `json:"ttl_seconds,omitempty" jsonschema:"optional TTL in seconds for the new mock"`
}

func registerTrafficTools(s *sdkmcp.Server, deps Deps) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_traffic",
		Description: `List recorded traffic, filterable by host/path/status/since/limit. Example: {"limit":20}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ListTrafficIn) (*sdkmcp.CallToolResult, ListTrafficOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		filter, err := trafficFilterFromIn(in)
		if err != nil {
			return nil, ListTrafficOut{}, explainErr(err)
		}
		list, err := deps.ListTraffic.Execute(ctx, partition, filter)
		if err != nil {
			return nil, ListTrafficOut{}, explainErr(err)
		}
		return nil, ListTrafficOut{Traffic: toSummaries(list)}, nil
	})

	getTrafficOutSchema, err := jsonschema.For[dto.TrafficDetailDTO](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			reflect.TypeFor[[]byte](): {Types: []string{"null", "string"}, ContentEncoding: "base64"},
		},
	})
	if err != nil {
		panic(fmt.Sprintf("mcp: building get_traffic output schema: %v", err))
	}
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:         "get_traffic",
		Description:  `Fetch one recorded interaction's full request+response (decrypted). Example: {"id":"<traffic-id>"}`,
		OutputSchema: getTrafficOutSchema,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in GetTrafficIn) (*sdkmcp.CallToolResult, dto.TrafficDetailDTO, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		t, err := deps.GetTraffic.Execute(ctx, partition, in.ID)
		if err != nil {
			return nil, dto.TrafficDetailDTO{}, explainErr(err)
		}
		req, err := usecase.DecodeRecordedMessage(t.Request)
		if err != nil {
			return nil, dto.TrafficDetailDTO{}, explainErr(err)
		}
		resp, err := usecase.DecodeRecordedMessage(t.Response)
		if err != nil {
			return nil, dto.TrafficDetailDTO{}, explainErr(err)
		}
		return nil, dto.TrafficDetailDTO{
			TrafficSummaryDTO: dto.TrafficToSummaryDTO(t),
			Request:           dto.RecordedMessageToDTO(req),
			Response:          dto.RecordedMessageToDTO(resp),
		}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "inspect_requests",
		Description: "List the most recent recorded requests, unfiltered — useful for debugging why a " +
			`mock didn't match. Example: {"limit":20}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in InspectRequestsIn) (*sdkmcp.CallToolResult, ListTrafficOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		list, err := deps.ListTraffic.Execute(ctx, partition, usecase.TrafficFilter{Limit: limit})
		if err != nil {
			return nil, ListTrafficOut{}, explainErr(err)
		}
		return nil, ListTrafficOut{Traffic: toSummaries(list)}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "metrics",
		Description: `Aggregate counts and latency by mock/path/status over a window. Example: {"window":"1h"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in MetricsIn) (*sdkmcp.CallToolResult, MetricsOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		var window time.Duration
		if in.Window != "" {
			d, err := time.ParseDuration(in.Window)
			if err != nil {
				return nil, MetricsOut{}, explainErr(fmt.Errorf("%w: invalid window %q — use a Go duration string like \"1h\" or \"30m\": %w", domain.ErrInvalidTrafficFilter, in.Window, err))
			}
			window = d
		}
		out, err := deps.Metrics.Execute(ctx, usecase.MetricsInput{Partition: partition, Window: window})
		if err != nil {
			return nil, MetricsOut{}, explainErr(err)
		}
		buckets := make([]MetricBucketDTO, len(out.Buckets))
		for i, b := range out.Buckets {
			buckets[i] = MetricBucketDTO{
				MockID: b.MockID, Path: b.Path, Status: b.Status, Count: b.Count,
				AvgLatencyMS: b.AvgLatencyMS, P95LatencyMS: b.P95LatencyMS,
			}
		}
		return nil, MetricsOut{Window: in.Window, Total: out.Total, Buckets: buckets}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "clear_traffic",
		Description: `Delete every recorded traffic entry in a space. Example: {}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ClearTrafficIn) (*sdkmcp.CallToolResult, ClearTrafficOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		if err := deps.ClearTraffic.Execute(ctx, partition); err != nil {
			return nil, ClearTrafficOut{}, explainErr(err)
		}
		return nil, ClearTrafficOut{Cleared: true}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "promote_traffic",
		Description: "Turn a recorded interaction into a persistent mock reproducing it (full fidelity for " +
			"status/body/single-valued headers; multi-valued headers are comma-joined). " +
			`Example: {"traffic_id":"<traffic-id>"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in PromoteTrafficIn) (*sdkmcp.CallToolResult, dto.MockDTO, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		m, err := deps.PromoteTraffic.Execute(ctx, usecase.PromoteTrafficInput{
			Partition: partition, TrafficID: in.TrafficID, Name: in.Name, TTLSeconds: in.TTLSeconds,
		})
		if err != nil {
			return nil, dto.MockDTO{}, explainErr(err)
		}
		return nil, dto.MockToDTO(m), nil
	})
}

func trafficFilterFromIn(in ListTrafficIn) (usecase.TrafficFilter, error) {
	filter := usecase.TrafficFilter{Method: in.Method, Host: in.Host, PathPrefix: in.Path, Status: in.Status, Limit: in.Limit}
	if in.Since != "" {
		since, err := time.Parse(time.RFC3339, in.Since)
		if err != nil {
			return usecase.TrafficFilter{}, fmt.Errorf("%w: invalid since %q — use RFC3339 (e.g. 2026-01-02T15:04:05Z): %w", domain.ErrInvalidTrafficFilter, in.Since, err)
		}
		filter.Since = &since
	}
	if in.Until != "" {
		until, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return usecase.TrafficFilter{}, fmt.Errorf("%w: invalid until %q — use RFC3339 (e.g. 2026-01-02T15:04:05Z): %w", domain.ErrInvalidTrafficFilter, in.Until, err)
		}
		filter.Until = &until
	}
	return filter, nil
}

func toSummaries(list []domain.TrafficRecord) []dto.TrafficSummaryDTO {
	out := make([]dto.TrafficSummaryDTO, len(list))
	for i, t := range list {
		out[i] = dto.TrafficToSummaryDTO(t)
	}
	return out
}
