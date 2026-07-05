package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

type fakeIssueTokenUseCase struct {
	gotClientKey string
	token        string
	err          error
}

func (f *fakeIssueTokenUseCase) Execute(_ context.Context, clientKey string) (string, error) {
	f.gotClientKey = clientKey
	return f.token, f.err
}

func TestIssueTokenReturnsTheMintedToken(t *testing.T) {
	uc := &fakeIssueTokenUseCase{token: "minted-token"}
	rr := httptest.NewRecorder()
	IssueToken(uc)(rr, newPostRequest(t, "/__lyrebird/auth/token", `{"client_key":"secret-1"}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	if uc.gotClientKey != "secret-1" {
		t.Errorf("use case received client_key %q, want %q", uc.gotClientKey, "secret-1")
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["token"] != "minted-token" {
		t.Errorf("response = %+v, want the minted token", out)
	}
}

func TestIssueTokenRejectsMalformedJSONBody(t *testing.T) {
	uc := &fakeIssueTokenUseCase{}
	rr := httptest.NewRecorder()
	IssueToken(uc)(rr, newPostRequest(t, "/__lyrebird/auth/token", `not json`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rr.Code)
	}
}

func TestIssueTokenMapsInvalidClientKeyToUnauthorized(t *testing.T) {
	uc := &fakeIssueTokenUseCase{err: domain.ErrInvalidClientKey}
	rr := httptest.NewRecorder()
	IssueToken(uc)(rr, newPostRequest(t, "/__lyrebird/auth/token", `{"client_key":"wrong-key"}`))

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an invalid client_key", rr.Code)
	}
	if got := rr.Body.String(); strings.Contains(got, "wrong-key") {
		t.Errorf("error response %q unexpectedly echoes the rejected client_key", got)
	}
}
