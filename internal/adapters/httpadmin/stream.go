package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// The Admin REST twin of the byte-stream plane's MCP tools. It is a thin
// twin: every handler decodes, delegates and encodes, with no logic of its
// own, and exposes nothing the MCP tools lack (constitution Principle II).
//
// Stream RULES need nothing here — a stream rule is an ordinary mock, served
// by the existing /__lyrebird/mocks endpoints (003's FR-021).

type endpointsUseCase interface {
	Create(ctx context.Context, in usecase.EndpointInput) (domain.Endpoint, error)
	List(ctx context.Context, partition string) ([]usecase.EndpointView, error)
	Delete(ctx context.Context, partition, name string) error
}

type emitFrameUseCase interface {
	Execute(ctx context.Context, in usecase.EmitFrameInput) error
}

// EmitFrameRequest is the body of POST /__lyrebird/stream/emit. Frame is the
// same declarative frame grammar a mock's respond body uses.
type EmitFrameRequest struct {
	Endpoint string `json:"endpoint"`
	Frame    string `json:"frame"`
}

// ListEndpoints handles GET /__lyrebird/stream/endpoints.
func ListEndpoints(uc endpointsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		views, err := uc.List(r.Context(), httpmw.PartitionFromContext(r.Context()))
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		out := make([]dto.EndpointDTO, len(views))
		for i, v := range views {
			out[i] = dto.EndpointToDTO(v.Endpoint, v.Occupied)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CreateEndpoint handles POST /__lyrebird/stream/endpoints.
func CreateEndpoint(uc endpointsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var d dto.EndpointDTO
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		in, err := dto.EndpointInputFromDTO(httpmw.PartitionFromContext(r.Context()), d)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		e, err := uc.Create(r.Context(), in)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, dto.EndpointToDTO(e, false))
	}
}

// DeleteEndpoint handles DELETE /__lyrebird/stream/endpoints/{name}.
func DeleteEndpoint(uc endpointsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := uc.Delete(r.Context(), httpmw.PartitionFromContext(r.Context()), r.PathValue("name")); err != nil {
			writeUseCaseError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// EmitFrame handles POST /__lyrebird/stream/emit.
func EmitFrame(uc emitFrameUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body EmitFrameRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		err := uc.Execute(r.Context(), usecase.EmitFrameInput{
			Partition: httpmw.PartitionFromContext(r.Context()),
			Endpoint:  body.Endpoint,
			FrameSpec: []byte(body.Frame),
		})
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
