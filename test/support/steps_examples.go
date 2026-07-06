package support

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cucumber/godog"
)

// examplesState drives examples.feature's REST-side assertions against the
// shared appState's control plane.
type examplesState struct {
	s *appState

	lastListStatus int
	lastListBody   []byte

	lastGetStatus int
	lastGetBody   []byte

	lastPostedMockStatus int
}

func (e *examplesState) doGET(ctx context.Context, path string) (int, []byte, error) {
	url := fmt.Sprintf("http://%s%s", e.s.app.ControlAddr(), path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build GET %s request: %w", path, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read GET %s response body: %w", path, err)
	}
	return resp.StatusCode, body, nil
}

func (e *examplesState) iListExamplesOverRESTWithNoQuery(ctx context.Context) error {
	status, body, err := e.doGET(ctx, "/__lyrebird/examples")
	if err != nil {
		return err
	}
	e.lastListStatus, e.lastListBody = status, body
	return nil
}

func (e *examplesState) iListExamplesOverRESTWithQuery(ctx context.Context, query string) error {
	status, body, err := e.doGET(ctx, fmt.Sprintf("/__lyrebird/examples?query=%s", query))
	if err != nil {
		return err
	}
	e.lastListStatus, e.lastListBody = status, body
	return nil
}

func (e *examplesState) theExampleListHasNEntries(n int) error {
	if e.lastListStatus != http.StatusOK {
		return fmt.Errorf("list examples status = %d, want 200 (body: %s)", e.lastListStatus, e.lastListBody)
	}
	var list []map[string]any
	if err := json.Unmarshal(e.lastListBody, &list); err != nil {
		return fmt.Errorf("decode example list: %w (body: %s)", err, e.lastListBody)
	}
	if len(list) != n {
		return fmt.Errorf("example list has %d entries, want %d (body: %s)", len(list), n, e.lastListBody)
	}
	return nil
}

func (e *examplesState) noListedExampleIncludesAMockField() error {
	var list []map[string]any
	if err := json.Unmarshal(e.lastListBody, &list); err != nil {
		return fmt.Errorf("decode example list: %w", err)
	}
	for _, entry := range list {
		if _, ok := entry["mock"]; ok {
			return fmt.Errorf("example summary %+v unexpectedly includes a mock field", entry)
		}
	}
	return nil
}

func (e *examplesState) iGetExampleOverREST(ctx context.Context, id string) error {
	status, body, err := e.doGET(ctx, "/__lyrebird/examples/"+id)
	if err != nil {
		return err
	}
	e.lastGetStatus, e.lastGetBody = status, body
	return nil
}

func (e *examplesState) theExampleResponseStatusIs(want int) error {
	if e.lastGetStatus != want {
		return fmt.Errorf("get example status = %d, want %d (body: %s)", e.lastGetStatus, want, e.lastGetBody)
	}
	return nil
}

func (e *examplesState) theFetchedExampleIncludesANonNullMockField() error {
	var doc map[string]any
	if err := json.Unmarshal(e.lastGetBody, &doc); err != nil {
		return fmt.Errorf("decode fetched example: %w", err)
	}
	mock, ok := doc["mock"]
	if !ok || mock == nil {
		return fmt.Errorf("fetched example %+v has no non-null mock field", doc)
	}
	return nil
}

func (e *examplesState) theFetchedExampleHasNoMockField() error {
	var doc map[string]any
	if err := json.Unmarshal(e.lastGetBody, &doc); err != nil {
		return fmt.Errorf("decode fetched example: %w", err)
	}
	if mock, ok := doc["mock"]; ok && mock != nil {
		return fmt.Errorf("fetched example unexpectedly has a mock field: %v", mock)
	}
	return nil
}

func (e *examplesState) iPostTheFetchedExamplesMockPayloadToMocks(ctx context.Context) error {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(e.lastGetBody, &doc); err != nil {
		return fmt.Errorf("decode fetched example: %w", err)
	}
	mock, ok := doc["mock"]
	if !ok {
		return fmt.Errorf("fetched example has no mock field to post")
	}
	url := fmt.Sprintf("http://%s/__lyrebird/mocks", e.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(mock))
	if err != nil {
		return fmt.Errorf("build create-mock request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/mocks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	e.lastPostedMockStatus = resp.StatusCode
	return nil
}

func (e *examplesState) theControlPlaneAcceptsThePostedMock() error {
	if e.lastPostedMockStatus != http.StatusOK && e.lastPostedMockStatus != http.StatusCreated {
		return fmt.Errorf("POST /__lyrebird/mocks status = %d, want 200/201", e.lastPostedMockStatus)
	}
	return nil
}

// RegisterExamplesSteps wires examples.feature's REST-side steps against the
// shared appState s.
func RegisterExamplesSteps(sc *godog.ScenarioContext, s *appState) {
	e := &examplesState{s: s}

	sc.Step(`^I list examples over REST with no query$`, e.iListExamplesOverRESTWithNoQuery)
	sc.Step(`^I list examples over REST with query "([^"]*)"$`, e.iListExamplesOverRESTWithQuery)
	sc.Step(`^the example list has (\d+) entries$`, e.theExampleListHasNEntries)
	sc.Step(`^no listed example includes a "mock" field$`, e.noListedExampleIncludesAMockField)

	sc.Step(`^I get example "([^"]*)" over REST$`, e.iGetExampleOverREST)
	sc.Step(`^the example response status is (\d+)$`, e.theExampleResponseStatusIs)
	sc.Step(`^the fetched example includes a non-null "mock" field$`, e.theFetchedExampleIncludesANonNullMockField)
	sc.Step(`^the fetched example has no "mock" field$`, e.theFetchedExampleHasNoMockField)

	sc.Step(`^I POST the fetched example's mock payload to /__lyrebird/mocks$`, e.iPostTheFetchedExamplesMockPayloadToMocks)
	sc.Step(`^the control plane accepts the posted mock$`, e.theControlPlaneAcceptsThePostedMock)
}
