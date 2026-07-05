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

// authState drives auth.feature's REST-side assertions and token-issuance
// steps; MCP-side connect-with-token steps live in steps_mcp.go instead.
type authState struct {
	s *appState

	lastRESTStatus int
	lastRESTBody   []byte

	lastTokenReqStatus int
	lastTokenReqBody   []byte
}

// bearerRoundTripper attaches "Authorization: Bearer <token>" to every
// request before delegating to base.
type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (t *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

func (a *authState) doREST(ctx context.Context, method, path, token string) error {
	url := fmt.Sprintf("http://%s%s", a.s.app.ControlAddr(), path)
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return fmt.Errorf("build %s %s request: %w", method, path, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s %s response body: %w", method, path, err)
	}
	a.lastRESTStatus, a.lastRESTBody = resp.StatusCode, body
	return nil
}

func (a *authState) iCallRESTWithNoBearerToken(ctx context.Context, method, path string) error {
	return a.doREST(ctx, method, path, "")
}

func (a *authState) iCallRESTWithBearerToken(ctx context.Context, method, path, token string) error {
	return a.doREST(ctx, method, path, token)
}

func (a *authState) iCallRESTWithTheIssuedBearerToken(ctx context.Context, method, path string) error {
	if a.s.lastIssuedToken == "" {
		return fmt.Errorf("no control-plane token has been issued yet")
	}
	return a.doREST(ctx, method, path, a.s.lastIssuedToken)
}

func (a *authState) theControlPlaneCallRespondsWithStatus(want int) error {
	if a.lastRESTStatus != want {
		return fmt.Errorf("control-plane call status = %d, want %d (body: %s)", a.lastRESTStatus, want, a.lastRESTBody)
	}
	return nil
}

func (a *authState) iRequestAControlPlaneTokenWithClientKey(ctx context.Context, clientKey string) error {
	payload, err := json.Marshal(map[string]string{"client_key": clientKey})
	if err != nil {
		return fmt.Errorf("marshal token request: %w", err)
	}
	url := fmt.Sprintf("http://%s/__lyrebird/auth/token", a.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/auth/token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read token response body: %w", err)
	}
	a.lastTokenReqStatus, a.lastTokenReqBody = resp.StatusCode, body

	if resp.StatusCode == http.StatusOK {
		var out struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return fmt.Errorf("decode token response: %w", err)
		}
		a.s.lastIssuedToken = out.Token
	}
	return nil
}

func (a *authState) theTokenRequestSucceeds() error {
	if a.lastTokenReqStatus != http.StatusOK {
		return fmt.Errorf("token request status = %d, want 200 (body: %s)", a.lastTokenReqStatus, a.lastTokenReqBody)
	}
	if a.s.lastIssuedToken == "" {
		return fmt.Errorf("token request succeeded but no token was captured in the response")
	}
	return nil
}

func (a *authState) theTokenRequestFailsWithStatus(want int) error {
	if a.lastTokenReqStatus != want {
		return fmt.Errorf("token request status = %d, want %d (body: %s)", a.lastTokenReqStatus, want, a.lastTokenReqBody)
	}
	return nil
}

func (a *authState) theTokenErrorResponseDoesNotContain(want string) error {
	if bytes.Contains(a.lastTokenReqBody, []byte(want)) {
		return fmt.Errorf("token error response unexpectedly contains %q: %s", want, a.lastTokenReqBody)
	}
	return nil
}

// RegisterAuthSteps wires auth.feature's steps against the shared appState
// s (other step files register the scenarios' remaining steps).
func RegisterAuthSteps(sc *godog.ScenarioContext, s *appState) {
	a := &authState{s: s}

	sc.Step(`^I call REST "(GET|POST|PUT|DELETE) ([^"]+)" with no bearer token$`, a.iCallRESTWithNoBearerToken)
	sc.Step(`^I call REST "(GET|POST|PUT|DELETE) ([^"]+)" with bearer token "([^"]*)"$`, a.iCallRESTWithBearerToken)
	sc.Step(`^I call REST "(GET|POST|PUT|DELETE) ([^"]+)" with the issued bearer token$`, a.iCallRESTWithTheIssuedBearerToken)
	sc.Step(`^the control-plane call responds with status (\d+)$`, a.theControlPlaneCallRespondsWithStatus)

	sc.Step(`^I request a control-plane token with client_key "([^"]*)"$`, a.iRequestAControlPlaneTokenWithClientKey)
	sc.Step(`^the token request succeeds$`, a.theTokenRequestSucceeds)
	sc.Step(`^the token request fails with status (\d+)$`, a.theTokenRequestFailsWithStatus)
	sc.Step(`^the token error response does not contain "([^"]*)"$`, a.theTokenErrorResponseDoesNotContain)
}
