package support

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// advancedProxyState carries advanced_proxy.feature's own fixtures on top of
// the shared appState.
type advancedProxyState struct {
	s *appState

	lastResp          *http.Response
	lastRespBody      []byte
	lastErr           error
	elapsed           time.Duration
	lastClientTimeout time.Duration
}

func (a *advancedProxyState) aProxyMockNamedMatchingPathWithRewriteRequestScript(
	ctx context.Context, name, method, path, src string,
) error {
	return createMockDTO(ctx, a.s, mockDTO{
		Name: name, Match: matchDTO{Method: method, Path: path},
		Action: actionDTO{Proxy: &proxyDTO{RewriteRequestScript: &src}},
	})
}

func (a *advancedProxyState) aProxyMockNamedMatchingPathWithTransformResponseScript(
	ctx context.Context, name, method, path, src string,
) error {
	return createMockDTO(ctx, a.s, mockDTO{
		Name: name, Match: matchDTO{Method: method, Path: path},
		Action: actionDTO{Proxy: &proxyDTO{TransformResponseScript: &src}},
	})
}

func (a *advancedProxyState) theFakeUpstreamReceivedPath(want string) error {
	if a.s.lastFakeUpstream == nil {
		return fmt.Errorf("no fake upstream server has been set up yet")
	}
	got := a.s.lastFakeUpstream.LastReceivedPath()
	if got != want {
		return fmt.Errorf("fake upstream received path = %q, want %q", got, want)
	}
	return nil
}

func (a *advancedProxyState) theFakeUpstreamReceivedHeaderEquals(name, want string) error {
	if a.s.lastFakeUpstream == nil {
		return fmt.Errorf("no fake upstream server has been set up yet")
	}
	got := a.s.lastFakeUpstream.LastReceivedHeader(name)
	if got != want {
		return fmt.Errorf("fake upstream received header %q = %q, want %q", name, got, want)
	}
	return nil
}

func (a *advancedProxyState) aMockNamedMatchingPathWithFaultDelay(
	ctx context.Context, name, method, path string, delayMs int,
) error {
	return createMockDTO(ctx, a.s, mockDTO{
		Name: name, Match: matchDTO{Method: method, Path: path},
		Action: actionDTO{Fault: &faultDTO{Kind: "delay", DelayMS: &delayMs}},
	})
}

func (a *advancedProxyState) aMockNamedMatchingPathWithFaultKind(
	ctx context.Context, name, method, path, kind string,
) error {
	return createMockDTO(ctx, a.s, mockDTO{
		Name: name, Match: matchDTO{Method: method, Path: path},
		Action: actionDTO{Fault: &faultDTO{Kind: kind}},
	})
}

func (a *advancedProxyState) aScenarioMockNamedMatchingPathWithResponsesAndOnExhaust(
	ctx context.Context, name, method, path, responsesCSV, onExhaust string,
) error {
	var responses []respondDTO
	for _, body := range strings.Split(responsesCSV, ",") {
		responses = append(responses, respondDTO{Status: 200, Body: body})
	}
	return createMockDTO(ctx, a.s, mockDTO{
		Name: name, Match: matchDTO{Method: method, Path: path},
		Action:   actionDTO{Respond: &respondDTO{Status: 200, Body: ""}},
		Scenario: &scenarioDTO{Responses: responses, OnExhaust: onExhaust},
	})
}

func (a *advancedProxyState) doSend(ctx context.Context, path, host string, timeout time.Duration) {
	a.lastClientTimeout = timeout
	client := &http.Client{Timeout: timeout}
	url := fmt.Sprintf("http://%s%s", a.s.app.DataAddr(), path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		a.lastErr = err
		return
	}
	req.Host = host

	start := time.Now()
	resp, err := client.Do(req)
	a.elapsed = time.Since(start)
	if err != nil {
		a.lastErr, a.lastResp, a.lastRespBody = err, nil, nil
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	a.lastErr, a.lastResp, a.lastRespBody = nil, resp, body
}

func (a *advancedProxyState) iSendATimedGETRequestToOnTheDataPlaneWithHost(ctx context.Context, path, host string) error {
	a.doSend(ctx, path, host, 5*time.Second)
	if a.lastErr != nil {
		return fmt.Errorf("send timed request: %w", a.lastErr)
	}
	return nil
}

func (a *advancedProxyState) iSendAGETRequestToOnTheDataPlaneWithHostWithAClientTimeoutOf(
	ctx context.Context, path, host, timeoutStr string,
) error {
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return fmt.Errorf("parse client timeout %q: %w", timeoutStr, err)
	}
	a.doSend(ctx, path, host, timeout)
	return nil // outcome asserted by a later step, not here
}

func (a *advancedProxyState) theTimedRequestSucceededWithStatus(want int) error {
	if a.lastResp == nil {
		return fmt.Errorf("timed request has no response: %w", a.lastErr)
	}
	if a.lastResp.StatusCode != want {
		return fmt.Errorf("timed request status = %d, want %d", a.lastResp.StatusCode, want)
	}
	return nil
}

func (a *advancedProxyState) theRequestTookAtLeast(want string) error {
	parsed, err := time.ParseDuration(want)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", want, err)
	}
	if a.elapsed < parsed {
		return fmt.Errorf("request took %v, want at least %v", a.elapsed, parsed)
	}
	return nil
}

// theRequestFailsWithAMalformedResponseError asserts not just that the
// request failed, but that it failed for the right reason: the client
// choked while parsing the garbage bytes as an HTTP response. Go's
// net/http reports this as one of a small family of errors all containing
// "malformed HTTP" (e.g. "malformed HTTP status code", "malformed HTTP
// response", "malformed HTTP version" — see net/http/response.go),
// confirmed empirically against the "not a valid HTTP response\r\n" bytes
// hijackAndWriteGarbage writes. This distinguishes the malformed fault from
// a plain reset or timeout, which fail with a different error entirely and
// would not contain this substring.
func (a *advancedProxyState) theRequestFailsWithAMalformedResponseError() error {
	if a.lastErr == nil {
		return fmt.Errorf("request succeeded, want it to fail")
	}
	if !strings.Contains(strings.ToLower(a.lastErr.Error()), "malformed http") {
		return fmt.Errorf("request failed with %q, want an error containing \"malformed HTTP\"", a.lastErr.Error())
	}
	return nil
}

func (a *advancedProxyState) theRequestFailsWithin(want string) error {
	if a.lastErr == nil {
		return fmt.Errorf("request succeeded, want it to fail")
	}
	parsed, err := time.ParseDuration(want)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", want, err)
	}
	if a.elapsed > parsed {
		return fmt.Errorf("request failed after %v, want within %v", a.elapsed, parsed)
	}
	return nil
}

// theRequestFailsWithAConnectionResetError asserts not just that the request
// failed fast, but that it failed for the right reason: a genuine TCP RST
// (hijackAndReset's SetLinger(0) close), not some other fast failure with
// the same timing profile. Empirically confirmed against this exact fault
// path (a temporary debug print of lastErr.Error(), since removed): Go's
// net/http surfaces this as an error containing "connection reset by peer"
// (e.g. `Get "http://...": read tcp ...: read: connection reset by peer`).
// Also asserting the error does NOT contain "malformed http" distinguishes
// this from FaultMalformed's garbage-bytes failure (see
// theRequestFailsWithAMalformedResponseError) — both fail fast, but for
// different reasons, so if FaultReset and FaultMalformed's handlers were
// ever swapped in fault.go's switch statement, this scenario would catch it.
// Folds in the same elapsed-time check as theRequestFailsWithin so the
// "closes immediately" guarantee isn't lost.
func (a *advancedProxyState) theRequestFailsWithAConnectionResetError(want string) error {
	if a.lastErr == nil {
		return fmt.Errorf("request succeeded, want it to fail")
	}
	errText := strings.ToLower(a.lastErr.Error())
	if strings.Contains(errText, "malformed http") {
		return fmt.Errorf("request failed with %q, want a connection-reset error, not a malformed-response error", a.lastErr.Error())
	}
	if !strings.Contains(errText, "connection reset by peer") {
		return fmt.Errorf("request failed with %q, want an error containing \"connection reset by peer\"", a.lastErr.Error())
	}
	parsed, err := time.ParseDuration(want)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", want, err)
	}
	if a.elapsed > parsed {
		return fmt.Errorf("request failed after %v, want within %v", a.elapsed, parsed)
	}
	return nil
}

// theRequestTimesOutWithoutAnyResponse asserts the client's own configured
// timeout is what ended the call (elapsed close to the full configured
// timeout), distinguishing a genuine hang from a fast connection-reset —
// which errors almost immediately, well under the configured timeout.
func (a *advancedProxyState) theRequestTimesOutWithoutAnyResponse() error {
	if a.lastErr == nil {
		return fmt.Errorf("request succeeded, want it to time out")
	}
	if a.elapsed < a.lastClientTimeout*8/10 {
		return fmt.Errorf("request failed after only %v (client timeout was %v) — too fast to be a genuine hang", a.elapsed, a.lastClientTimeout)
	}
	return nil
}

// RegisterAdvancedProxySteps wires advanced_proxy.feature's steps against
// the shared appState s. Mock creation reuses createMockDTO from
// steps_mock.go; response/traffic assertions and reset are reused verbatim
// from steps_spy.go/steps_lifetimes.go.
func RegisterAdvancedProxySteps(sc *godog.ScenarioContext, s *appState) {
	a := &advancedProxyState{s: s}

	sc.Step(`^a proxy mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" with rewrite_request script "((?:[^"\\]|\\.)*)"$`,
		func(ctx context.Context, name, method, path, src string) error {
			return a.aProxyMockNamedMatchingPathWithRewriteRequestScript(ctx, name, method, path, unescapeStep(src))
		})
	sc.Step(`^a proxy mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" with transform_response script "((?:[^"\\]|\\.)*)"$`,
		func(ctx context.Context, name, method, path, src string) error {
			return a.aProxyMockNamedMatchingPathWithTransformResponseScript(ctx, name, method, path, unescapeStep(src))
		})
	sc.Step(`^the fake upstream received path "([^"]*)"$`, a.theFakeUpstreamReceivedPath)
	sc.Step(`^the fake upstream received header "([^"]*)" equals "([^"]*)"$`, a.theFakeUpstreamReceivedHeaderEquals)

	sc.Step(`^a mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" with fault delay (\d+)ms$`,
		a.aMockNamedMatchingPathWithFaultDelay)
	sc.Step(`^a mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" with fault kind "([^"]*)"$`,
		a.aMockNamedMatchingPathWithFaultKind)
	sc.Step(`^a scenario mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" with responses "([^"]*)" and on_exhaust "([^"]*)"$`,
		a.aScenarioMockNamedMatchingPathWithResponsesAndOnExhaust)

	sc.Step(`^I send a timed GET request to "([^"]*)" on the data plane with host "([^"]*)"$`,
		a.iSendATimedGETRequestToOnTheDataPlaneWithHost)
	sc.Step(`^I send a GET request to "([^"]*)" on the data plane with host "([^"]*)" with a client timeout of "([^"]*)"$`,
		a.iSendAGETRequestToOnTheDataPlaneWithHostWithAClientTimeoutOf)
	sc.Step(`^the timed request succeeded with status (\d+)$`, a.theTimedRequestSucceededWithStatus)
	sc.Step(`^the request took at least "([^"]*)"$`, a.theRequestTookAtLeast)
	sc.Step(`^the request fails with a malformed response error$`, a.theRequestFailsWithAMalformedResponseError)
	sc.Step(`^the request fails within "([^"]*)"$`, a.theRequestFailsWithin)
	sc.Step(`^the request fails with a connection reset error within "([^"]*)"$`, a.theRequestFailsWithAConnectionResetError)
	sc.Step(`^the request times out without any response$`, a.theRequestTimesOutWithoutAnyResponse)
}

// unescapeStep undoes the backslash-escaping a Gherkin step needs for a JS
// script literal containing double quotes (the step pattern itself is
// delimited by unescaped double quotes).
func unescapeStep(s string) string {
	return strings.ReplaceAll(s, `\"`, `"`)
}
