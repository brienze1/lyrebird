package dto

import (
	"net/textproto"

	"github.com/brienze1/lyrebird/internal/usecase"
)

// MatchTestRequestDTO is the wire shape of a match-test sample request.
type MatchTestRequestDTO struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers,omitempty"`
	Query   map[string][]string `json:"query,omitempty"`
	Body    string              `json:"body,omitempty"`
}

// ConditionResultDTO is the wire shape of usecase.ConditionResult.
type ConditionResultDTO struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Passed   bool   `json:"passed"`
}

// CandidateResultDTO is the wire shape of usecase.CandidateResult.
type CandidateResultDTO struct {
	Mock       MockDTO              `json:"mock"`
	Matched    bool                 `json:"matched"`
	Conditions []ConditionResultDTO `json:"conditions"`
}

// MatchTestResponseDTO is the wire shape of usecase.MatchTestOutput.
type MatchTestResponseDTO struct {
	Candidates []CandidateResultDTO `json:"candidates"`
	Winner     *MockDTO             `json:"winner,omitempty"`
	Status     int                  `json:"status,omitempty"`
	Headers    map[string]string    `json:"headers,omitempty"`
	Body       string               `json:"body,omitempty"`
}

// CanonicalizeHeaders normalizes submitted header keys the same way
// net/http does on the live data-plane path (usecase.MatchInput.Header
// there is built directly from r.Header, which net/http already
// canonicalizes on parse). Without this, a match-test submission of e.g.
// "x-vip" would miss a condition on "X-VIP" that live traffic —
// canonicalized to "X-Vip" by net/http — actually matches, making the
// dry-run an unfaithful predictor.
func CanonicalizeHeaders(h map[string][]string) map[string][]string {
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

// MatchTestInputFromDTO converts a MatchTestRequestDTO to a usecase.MatchInput.
func MatchTestInputFromDTO(req MatchTestRequestDTO) usecase.MatchInput {
	return usecase.MatchInput{
		Method: req.Method, Path: req.Path,
		Header: CanonicalizeHeaders(req.Headers), Query: req.Query, Body: []byte(req.Body),
	}
}

// MatchTestOutputToDTO converts a usecase.MatchTestOutput to its wire equivalent.
func MatchTestOutputToDTO(out usecase.MatchTestOutput) MatchTestResponseDTO {
	resp := MatchTestResponseDTO{Candidates: make([]CandidateResultDTO, len(out.Candidates))}
	for i, c := range out.Candidates {
		conditions := make([]ConditionResultDTO, len(c.Conditions))
		for j, cond := range c.Conditions {
			conditions[j] = ConditionResultDTO{Field: cond.Field, Expected: cond.Expected, Actual: cond.Actual, Passed: cond.Passed}
		}
		resp.Candidates[i] = CandidateResultDTO{Mock: MockToDTO(c.Mock), Matched: c.Matched, Conditions: conditions}
	}
	if out.Winner != nil {
		winnerDTO := MockToDTO(*out.Winner)
		resp.Winner = &winnerDTO
		resp.Status, resp.Headers, resp.Body = out.Status, out.Headers, string(out.Body)
	}
	return resp
}
