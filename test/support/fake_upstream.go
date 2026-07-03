package support

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// FakeUpstream is a small httptest.Server-backed test double standing in
// for "the real API" spy tests forward to.
type FakeUpstream struct {
	srv *httptest.Server

	mu              sync.Mutex
	status          int
	body            []byte
	headers         map[string]string
	hang            time.Duration
	echo            bool
	lastReceivedLen int
	requestCount    int
}

// NewFakeUpstream starts a fake upstream responding 200 with an empty body
// by default.
func NewFakeUpstream() *FakeUpstream {
	f := &FakeUpstream{status: http.StatusOK}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

// URL returns the fake upstream's base URL (suitable as an Upstream.TargetURL).
func (f *FakeUpstream) URL() string { return f.srv.URL }

// Close shuts down the fake upstream.
func (f *FakeUpstream) Close() { f.srv.Close() }

// SetResponse configures a fixed response for subsequent requests.
func (f *FakeUpstream) SetResponse(status int, body []byte, headers map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body, f.headers = status, body, headers
}

// HangFor makes the fake upstream wait d before responding (or until the
// request's context is cancelled, whichever comes first) — used to exercise
// the upstream-timeout -> 504 path.
func (f *FakeUpstream) HangFor(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hang = d
}

// EchoRequestBody makes the fake upstream read the full request body, then
// respond with it verbatim (status 200), recording its received length.
// Reads to completion before writing any response byte — see the comment
// in handle for why writing early would silently truncate the request.
func (f *FakeUpstream) EchoRequestBody() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.echo = true
	f.status = http.StatusOK
}

// LastReceivedBodyLen returns the byte length of the most recently received
// request body.
func (f *FakeUpstream) LastReceivedBodyLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReceivedLen
}

// RequestCount returns how many requests this fake upstream has received so
// far — used to assert a matching mock short-circuits before ever reaching
// the upstream (SC-003).
func (f *FakeUpstream) RequestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requestCount
}

func (f *FakeUpstream) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requestCount++
	hang, echo, status, body, headers := f.hang, f.echo, f.status, f.body, f.headers
	f.mu.Unlock()

	if hang > 0 {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(hang):
		}
	}

	for k, v := range headers {
		w.Header().Set(k, v)
	}

	if echo {
		// Read the full request body BEFORE writing any response. Writing
		// response headers while the client is still uploading can make
		// Go's http.Transport treat it as an early response and abandon
		// the rest of the outbound body write (net/http's "request
		// completes before response starts" assumption) — that silently
		// truncated the very body this test double exists to prove flows
		// through in full.
		received, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		f.lastReceivedLen = len(received)
		f.mu.Unlock()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(received)
		return
	}

	n, _ := io.Copy(io.Discard, r.Body)
	f.mu.Lock()
	f.lastReceivedLen = int(n)
	f.mu.Unlock()

	w.WriteHeader(status)
	_, _ = w.Write(body)
}
