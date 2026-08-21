package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// The byte-stream data plane's control surface. There is no separate surface
// for stream RULES: a stream rule is an ordinary mock, so create_mock,
// update_mock, list_mocks, match_test and promote_traffic all apply to it
// unchanged (003's FR-021). Only the two things a mock cannot express — the
// boundary itself, and pushing bytes nothing asked for — get tools here.

// CreateEndpointIn is create_endpoint's input.
type CreateEndpointIn struct {
	Space      string             `json:"space,omitempty" jsonschema:"space/partition the endpoint belongs to; defaults to the server's default space"`
	Name       string             `json:"name" jsonschema:"endpoint name a stand-in claims in its handshake and a rule targets as match.path \"/<name>\"; no slashes or whitespace"`
	Framing    dto.FramingDTO     `json:"framing" jsonschema:"where one frame ends: exactly one of delimiter, delimiter_hex, length, or prefix"`
	Projection *dto.ProjectionDTO `json:"projection,omitempty" jsonschema:"default field projection for every rule on this endpoint; a rule may override it"`
	Cadence    *CadenceIn         `json:"cadence,omitempty" jsonschema:"frames emitted on an interval with nothing having asked for them"`
}

// CadenceIn is create_endpoint's cadence block.
//
// Its frames are frame-spec STRINGS rather than structured parts, for two
// reasons: the part grammar is self-referential (a repeat contains a part),
// which the MCP SDK's schema generator cannot express, and carrying the spec
// as a string means an agent writes the exact same grammar here, in a mock's
// respond body, and in emit_frame.
type CadenceIn struct {
	Interval     string   `json:"interval" jsonschema:"time between emissions as a duration string, e.g. 100ms; 0ms/0s means immediate — every frame queued back-to-back, paced only by the connection's own backpressure, never by a clock"`
	OnExhaustion string   `json:"on_exhaustion,omitempty" jsonschema:"what happens once the sequence runs out: repeat_last (default), loop, or stop"`
	Frames       []string `json:"frames" jsonschema:"frames emitted in order, one per interval; each a frame spec, e.g. [{\"text\":\"TICK,0001\"}]"`
}

// ListEndpointsIn is list_endpoints's input.
type ListEndpointsIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition to list; defaults to the server's default space"`
}

// EndpointOut is one endpoint as the MCP tools render it.
//
// It is not dto.EndpointDTO because that type reaches dto.FramePartDTO, which
// is self-referential through Repeat — and the MCP SDK's schema generator
// rejects a recursive type outright. The cadence is therefore rendered with
// its frames as spec strings, the same grammar CadenceIn accepts and
// emit_frame takes, so what a tool returns can be handed straight back to one.
type EndpointOut struct {
	Name       string             `json:"name"`
	Framing    dto.FramingDTO     `json:"framing"`
	Projection *dto.ProjectionDTO `json:"projection,omitempty"`
	Cadence    *CadenceIn         `json:"cadence,omitempty"`
	Lifetime   string             `json:"lifetime,omitempty"`
	Occupied   bool               `json:"occupied,omitempty"`
}

// ListEndpointsOut is list_endpoints's output.
type ListEndpointsOut struct {
	Endpoints []EndpointOut `json:"endpoints"`
}

// endpointOut renders a domain endpoint for the MCP surface.
func endpointOut(e domain.Endpoint, occupied bool) (EndpointOut, error) {
	interval, onExhaustion, frames, err := dto.CadenceToSpecs(e.Cadence)
	if err != nil {
		return EndpointOut{}, err
	}
	out := EndpointOut{
		Name:       e.Name,
		Framing:    dto.FramingToDTO(e.Framing),
		Projection: dto.ProjectionToDTO(e.Projection),
		Lifetime:   string(e.Lifetime),
		Occupied:   occupied,
	}
	if e.Cadence != nil {
		out.Cadence = &CadenceIn{Interval: interval, OnExhaustion: onExhaustion, Frames: frames}
	}
	return out, nil
}

// DeleteEndpointIn is delete_endpoint's input.
type DeleteEndpointIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition the endpoint belongs to; defaults to the server's default space"`
	Name  string `json:"name" jsonschema:"name of the endpoint to remove; seeded endpoints cannot be removed"`
}

// EmitFrameIn is emit_frame's input.
type EmitFrameIn struct {
	Space    string `json:"space,omitempty" jsonschema:"space/partition the endpoint belongs to; defaults to the server's default space"`
	Endpoint string `json:"endpoint" jsonschema:"endpoint whose connected stand-in receives the frame"`
	Frame    string `json:"frame" jsonschema:"frame spec: a JSON list of parts, each one of text/hex/int/copyFrom/repeat, e.g. [{\"text\":\"TICK\"}]"`
}

// DeletedOut reports a successful removal.
type DeletedOut struct {
	Deleted bool `json:"deleted"`
}

// EmittedOut reports a successful emission.
type EmittedOut struct {
	Emitted bool `json:"emitted"`
}

func registerStreamTools(s *sdkmcp.Server, deps Deps) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "create_endpoint",
		Description: "Declare a byte-stream endpoint a peripheral stand-in can connect to over the stream data " +
			"plane (needs LYREBIRD_STREAM_PORT). Rules for it are ordinary mocks with match.path \"/<name>\". " +
			`Example: {"name":"widget","framing":{"delimiter":"\r\n"},"projection":{"split":","}}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in CreateEndpointIn) (*sdkmcp.CallToolResult, EndpointOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		framing, err := dto.FramingFromDTO(in.Framing)
		if err != nil {
			return nil, EndpointOut{}, explainErr(err)
		}
		var cadence *domain.Cadence
		if in.Cadence != nil {
			if cadence, err = dto.CadenceFromSpecs(in.Cadence.Interval, in.Cadence.OnExhaustion, in.Cadence.Frames); err != nil {
				return nil, EndpointOut{}, explainErr(err)
			}
		}
		e, err := deps.Endpoints.Create(ctx, usecase.EndpointInput{
			Partition: partition, Name: in.Name, Framing: framing,
			Projection: dto.ProjectionFromDTO(in.Projection), Cadence: cadence,
		})
		if err != nil {
			return nil, EndpointOut{}, explainErr(err)
		}
		out, err := endpointOut(e, false)
		if err != nil {
			return nil, EndpointOut{}, explainErr(err)
		}
		return nil, out, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "list_endpoints",
		Description: "List every byte-stream endpoint in a space, seeded and session-created alike, each with " +
			`whether a stand-in currently holds it. Example: {}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ListEndpointsIn) (*sdkmcp.CallToolResult, ListEndpointsOut, error) {
		views, err := deps.Endpoints.List(ctx, resolveSpace(in.Space, deps.DefaultSpace))
		if err != nil {
			return nil, ListEndpointsOut{}, explainErr(err)
		}
		out := ListEndpointsOut{Endpoints: make([]EndpointOut, 0, len(views))}
		for _, v := range views {
			e, err := endpointOut(v.Endpoint, v.Occupied)
			if err != nil {
				return nil, ListEndpointsOut{}, explainErr(err)
			}
			out.Endpoints = append(out.Endpoints, e)
		}
		return nil, out, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "delete_endpoint",
		Description: "Remove a session-created byte-stream endpoint and drop any stand-in connected to it. " +
			`Seeded endpoints are protected config and cannot be removed. Example: {"name":"widget"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in DeleteEndpointIn) (*sdkmcp.CallToolResult, DeletedOut, error) {
		if err := deps.Endpoints.Delete(ctx, resolveSpace(in.Space, deps.DefaultSpace), in.Name); err != nil {
			return nil, DeletedOut{}, explainErr(err)
		}
		return nil, DeletedOut{Deleted: true}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "emit_frame",
		Description: "Push a frame to the stand-in connected to a byte-stream endpoint, with no inbound frame " +
			"having asked for it — how a test produces a specific state mid-scenario. Errors when nothing is " +
			`connected. Example: {"endpoint":"widget","frame":"[{\"text\":\"TICK,0001\"}]"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in EmitFrameIn) (*sdkmcp.CallToolResult, EmittedOut, error) {
		err := deps.EmitFrame.Execute(ctx, usecase.EmitFrameInput{
			Partition: resolveSpace(in.Space, deps.DefaultSpace),
			Endpoint:  in.Endpoint,
			FrameSpec: []byte(in.Frame),
		})
		if err != nil {
			return nil, EmittedOut{}, explainErr(err)
		}
		return nil, EmittedOut{Emitted: true}, nil
	})
}
