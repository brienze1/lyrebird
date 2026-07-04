package usecase

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/brienze1/lyrebird/internal/domain"
)

// PromoteTraffic turns a recorded interaction into a persistent ephemeral
// mock reproducing it (FR-012, SC-005). Depends on the concrete *MockCRUD
// (same package/layer, not a layering violation) to reuse its existing
// create validation rather than re-implementing mock creation.
//
// Known fidelity limitation, documented rather than hidden:
// domain.RespondAction.Headers is map[string]string (single-valued, an M2
// decision) — a recorded response with multiple values for one header
// (e.g. Set-Cookie) is comma-joined here, which is not universally
// header-safe. Full fidelity holds for status/body/single-valued headers.
type PromoteTraffic struct {
	traffic TrafficRepo
	mocks   *MockCRUD
}

// NewPromoteTraffic builds a PromoteTraffic use case.
func NewPromoteTraffic(traffic TrafficRepo, mocks *MockCRUD) *PromoteTraffic {
	return &PromoteTraffic{traffic: traffic, mocks: mocks}
}

// PromoteTrafficInput carries PromoteTraffic.Execute's parameters.
type PromoteTrafficInput struct {
	Partition  string
	TrafficID  string
	Name       string // optional; defaults to "promoted-"+TrafficID
	TTLSeconds *int
}

// Execute looks up the recorded interaction, decodes its response, and
// creates a new ephemeral mock matching the recorded method+path exactly
// (host-agnostic, like every other mock) that responds with the recorded
// status/headers/body verbatim.
func (uc *PromoteTraffic) Execute(ctx context.Context, in PromoteTrafficInput) (domain.Mock, error) {
	rec, err := uc.traffic.GetTraffic(ctx, in.Partition, in.TrafficID)
	if err != nil {
		return domain.Mock{}, fmt.Errorf("usecase: promote traffic: get traffic: %w", err)
	}
	resp, err := DecodeRecordedMessage(rec.Response)
	if err != nil {
		return domain.Mock{}, fmt.Errorf("usecase: promote traffic: decode response: %w", err)
	}

	name := in.Name
	if name == "" {
		name = "promoted-" + rec.ID
	}

	// Regex-exact (not a bare glob string) so a recorded path containing
	// glob metacharacters ("*", "?", "[") still round-trips faithfully.
	match := domain.Match{Method: rec.Method, Path: "~^" + regexp.QuoteMeta(rec.Path) + "$"}

	headers := make(map[string]string, len(resp.Headers))
	for k, vs := range resp.Headers {
		if len(vs) > 0 {
			headers[k] = strings.Join(vs, ", ")
		}
	}
	action := domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{
		Status: rec.Status, Headers: headers, Body: resp.Body,
	}}

	return uc.mocks.Create(ctx, MockInput{
		Partition: in.Partition, Name: name, Match: match, Action: action, TTLSeconds: in.TTLSeconds,
	})
}
