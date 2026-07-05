package httpmw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeVerifier struct{ err error }

func (f fakeVerifier) Verify(token string) error {
	if token == "valid-token" {
		return nil
	}
	if f.err != nil {
		return f.err
	}
	return errors.New("invalid token")
}

func newNextCalledHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { *called = true })
}

func TestAuth_ExemptPathBypassesTheCheckEntirely(t *testing.T) {
	var nextCalled bool
	h := Auth(fakeVerifier{}, "/__lyrebird/healthz")(newNextCalledHandler(&nextCalled))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/__lyrebird/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !nextCalled {
		t.Fatalf("expected the exempt path to reach the next handler")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the wrapped handler never wrote a status)", w.Code)
	}
}

func TestAuth_MissingAuthorizationHeaderIsRejected(t *testing.T) {
	var nextCalled bool
	h := Auth(fakeVerifier{})(newNextCalledHandler(&nextCalled))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/__lyrebird/mocks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if nextCalled {
		t.Fatalf("expected the next handler NOT to run for a missing Authorization header")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAuth_NonBearerAuthorizationHeaderIsRejected(t *testing.T) {
	var nextCalled bool
	h := Auth(fakeVerifier{})(newNextCalledHandler(&nextCalled))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/__lyrebird/mocks", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if nextCalled {
		t.Fatalf("expected the next handler NOT to run for a non-Bearer Authorization header")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAuth_ATokenThatFailsVerifyIsRejected(t *testing.T) {
	var nextCalled bool
	h := Auth(fakeVerifier{})(newNextCalledHandler(&nextCalled))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/__lyrebird/mocks", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if nextCalled {
		t.Fatalf("expected the next handler NOT to run for a token that fails Verify")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAuth_AValidTokenReachesTheNextHandler(t *testing.T) {
	var nextCalled bool
	h := Auth(fakeVerifier{})(newNextCalledHandler(&nextCalled))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/__lyrebird/mocks", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !nextCalled {
		t.Fatalf("expected the next handler to run for a valid token")
	}
}
