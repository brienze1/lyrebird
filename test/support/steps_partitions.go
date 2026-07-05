package support

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"

	"github.com/brienze1/lyrebird/internal/usecase"
)

// partitionState carries partitions.feature's own fixtures on top of the
// shared appState. Mock creation reuses mockDTO/createMock's wire shape
// from steps_mock.go but adds the X-Lyrebird-Space header that plain
// createMock never sets (every existing mock_override.feature scenario
// only ever targets the default space).
type partitionState struct {
	s *appState

	lastStatus int
	lastSpaces []partitionDTO
}

// partitionDTO mirrors dto.PartitionDTO's wire shape for test authoring.
type partitionDTO struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}

func (p *partitionState) createMockInSpace(ctx context.Context, space string, dto mockDTO) error {
	raw, err := json.Marshal(dto)
	if err != nil {
		return fmt.Errorf("marshal mock dto: %w", err)
	}
	url := fmt.Sprintf("http://%s/__lyrebird/mocks", p.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build create-mock request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lyrebird-Space", space)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/mocks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("POST /__lyrebird/mocks (space %q) status = %d, want 200/201", space, resp.StatusCode)
	}
	return nil
}

func (p *partitionState) aMockNamedInSpaceMatchingPathThatRespondsWithBody(
	ctx context.Context, name, space, method, path string, status int, body string,
) error {
	return p.createMockInSpace(ctx, space, mockDTO{
		Name: name, Match: matchDTO{Method: method, Path: path},
		Action: actionDTO{Respond: &respondDTO{Status: status, Body: body}},
	})
}

func (p *partitionState) noTrafficIsRecordedInSpace(ctx context.Context, space string) error {
	list, err := p.s.app.Store.ListTraffic(ctx, space, usecase.TrafficFilter{})
	if err != nil {
		return fmt.Errorf("list traffic in space %q: %w", space, err)
	}
	if len(list) != 0 {
		return fmt.Errorf("space %q has %d traffic record(s), want 0", space, len(list))
	}
	return nil
}

func (p *partitionState) trafficIsRecordedInSpace(ctx context.Context, space string) error {
	list, err := p.s.app.Store.ListTraffic(ctx, space, usecase.TrafficFilter{})
	if err != nil {
		return fmt.Errorf("list traffic in space %q: %w", space, err)
	}
	if len(list) == 0 {
		return fmt.Errorf("space %q has 0 traffic record(s), want at least 1", space)
	}
	return nil
}

func (p *partitionState) iDeleteTheSpaceViaTheControlPlane(ctx context.Context, id string) error {
	return p.doDeleteSpace(ctx, id)
}

func (p *partitionState) iAttemptToDeleteTheSpaceViaTheControlPlane(ctx context.Context, id string) error {
	return p.doDeleteSpace(ctx, id)
}

func (p *partitionState) doDeleteSpace(ctx context.Context, id string) error {
	url := fmt.Sprintf("http://%s/__lyrebird/spaces/%s", p.s.app.ControlAddr(), id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("build delete-space request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE /__lyrebird/spaces/%s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	p.lastStatus = resp.StatusCode
	return nil
}

func (p *partitionState) iCreateASpaceWithDescriptionViaTheControlPlane(ctx context.Context, id, description string) error {
	raw, err := json.Marshal(partitionDTO{ID: id, Description: description})
	if err != nil {
		return fmt.Errorf("marshal partition dto: %w", err)
	}
	url := fmt.Sprintf("http://%s/__lyrebird/spaces", p.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build create-space request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/spaces: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	p.lastStatus = resp.StatusCode
	return nil
}

func (p *partitionState) iRequestTheListOfSpacesViaTheControlPlane(ctx context.Context) error {
	url := fmt.Sprintf("http://%s/__lyrebird/spaces", p.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build list-spaces request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET /__lyrebird/spaces: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /__lyrebird/spaces status = %d, want 200", resp.StatusCode)
	}
	var out []partitionDTO
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode list-spaces response: %w", err)
	}
	p.lastSpaces = out
	return nil
}

func (p *partitionState) theSpaceListIncludes(id string) error {
	for _, s := range p.lastSpaces {
		if s.ID == id {
			return nil
		}
	}
	return fmt.Errorf("space list %+v does not include %q", p.lastSpaces, id)
}

func (p *partitionState) theSpaceControlPlaneRespondsWithStatus(want int) error {
	if p.lastStatus != want {
		return fmt.Errorf("space control plane responded with status %d, want %d", p.lastStatus, want)
	}
	return nil
}

func (p *partitionState) spaceHasNoMocksTrafficOrUpstreamsLeft(ctx context.Context, space string) error {
	mocks, err := p.s.app.Store.ListMocks(ctx, space)
	if err != nil {
		return fmt.Errorf("list mocks in space %q: %w", space, err)
	}
	if len(mocks) != 0 {
		return fmt.Errorf("space %q has %d mock(s) left, want 0", space, len(mocks))
	}
	traffic, err := p.s.app.Store.ListTraffic(ctx, space, usecase.TrafficFilter{})
	if err != nil {
		return fmt.Errorf("list traffic in space %q: %w", space, err)
	}
	if len(traffic) != 0 {
		return fmt.Errorf("space %q has %d traffic record(s) left, want 0", space, len(traffic))
	}
	upstreams, err := p.s.app.Store.ListUpstreams(ctx, space)
	if err != nil {
		return fmt.Errorf("list upstreams in space %q: %w", space, err)
	}
	if len(upstreams) != 0 {
		return fmt.Errorf("space %q has %d upstream(s) left, want 0", space, len(upstreams))
	}
	return nil
}

// RegisterPartitionSteps wires partitions.feature's steps against the
// shared appState s. Request-sending, response, and per-request-traffic
// assertions are reused verbatim from steps_spy.go; mock creation reuses
// mockDTO/matchDTO/respondDTO/actionDTO from steps_mock.go.
func RegisterPartitionSteps(sc *godog.ScenarioContext, s *appState) {
	p := &partitionState{s: s}

	sc.Step(`^a mock named "([^"]*)" in space "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" that responds (\d+) with body "([^"]*)"$`,
		p.aMockNamedInSpaceMatchingPathThatRespondsWithBody)
	sc.Step(`^no traffic is recorded in space "([^"]*)"$`, p.noTrafficIsRecordedInSpace)
	sc.Step(`^traffic is recorded in space "([^"]*)"$`, p.trafficIsRecordedInSpace)
	sc.Step(`^I delete the space "([^"]*)" via the control plane$`, p.iDeleteTheSpaceViaTheControlPlane)
	sc.Step(`^I attempt to delete the space "([^"]*)" via the control plane$`, p.iAttemptToDeleteTheSpaceViaTheControlPlane)
	sc.Step(`^the space control plane responds with status (\d+)$`, p.theSpaceControlPlaneRespondsWithStatus)
	sc.Step(`^space "([^"]*)" has no mocks, traffic, or upstreams left$`, p.spaceHasNoMocksTrafficOrUpstreamsLeft)
	sc.Step(`^I create a space "([^"]*)" with description "([^"]*)" via the control plane$`, p.iCreateASpaceWithDescriptionViaTheControlPlane)
	sc.Step(`^I request the list of spaces via the control plane$`, p.iRequestTheListOfSpacesViaTheControlPlane)
	sc.Step(`^the space list includes "([^"]*)"$`, p.theSpaceListIncludes)
}
