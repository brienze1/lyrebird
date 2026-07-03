package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/textproto"

	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type matchTestRequestDTO struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers,omitempty"`
	Query   map[string][]string `json:"query,omitempty"`
	Body    string              `json:"body,omitempty"`
}

type conditionResultDTO struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Passed   bool   `json:"passed"`
}

type candidateResultDTO struct {
	Mock       mockDTO              `json:"mock"`
	Matched    bool                 `json:"matched"`
	Conditions []conditionResultDTO `json:"conditions"`
}

type matchTestResponseDTO struct {
	Candidates []candidateResultDTO `json:"candidates"`
	Winner     *mockDTO             `json:"winner,omitempty"`
	Status     int                  `json:"status,omitempty"`
	Headers    map[string]string    `json:"headers,omitempty"`
	Body       string               `json:"body,omitempty"`
}

type matchTester interface {
	Execute(ctx context.Context, partition string, in usecase.MatchInput) (usecase.MatchTestOutput, error)
}

// MatchTest handles POST /__lyrebird/match-test (contracts/admin-rest.md,
// FR-011): a dry-run reporting which mock would fire, every candidate's
// per-condition detail, and the resolved response — never forwarding
// anything onward.
func MatchTest(uc matchTester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req matchTestRequestDTO
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		partition := httpmw.PartitionFromContext(r.Context())
		in := usecase.MatchInput{Method: req.Method, Path: req.Path, Header: canonicalizeHeaders(req.Headers), Query: req.Query, Body: []byte(req.Body)}

		out, err := uc.Execute(r.Context(), partition, in)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, matchTestOutputToDTO(out))
	}
}

// canonicalizeHeaders normalizes submitted header keys the same way
// net/http does on the live data-plane path (MatchInput.Header there is
// built directly from r.Header, which net/http already canonicalizes on
// parse). Without this, a match-test submission of e.g. "x-vip" would miss
// a condition on "X-VIP" that live traffic — canonicalized to "X-Vip" by
// net/http — actually matches, making the dry-run an unfaithful predictor.
func canonicalizeHeaders(h map[string][]string) map[string][]string {
	if len(h) == 0 {
		return h
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		ck := textproto.CanonicalMIMEHeaderKey(k)
		out[ck] = append(out[ck], vs...)
	}
	return out
}

func matchTestOutputToDTO(out usecase.MatchTestOutput) matchTestResponseDTO {
	resp := matchTestResponseDTO{Candidates: make([]candidateResultDTO, len(out.Candidates))}
	for i, c := range out.Candidates {
		conditions := make([]conditionResultDTO, len(c.Conditions))
		for j, cond := range c.Conditions {
			conditions[j] = conditionResultDTO{Field: cond.Field, Expected: cond.Expected, Actual: cond.Actual, Passed: cond.Passed}
		}
		resp.Candidates[i] = candidateResultDTO{Mock: mockToDTO(c.Mock), Matched: c.Matched, Conditions: conditions}
	}
	if out.Winner != nil {
		winnerDTO := mockToDTO(*out.Winner)
		resp.Winner = &winnerDTO
		resp.Status, resp.Headers, resp.Body = out.Status, out.Headers, string(out.Body)
	}
	return resp
}
