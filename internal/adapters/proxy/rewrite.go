package proxy

import (
	"bytes"
	"io"
	"net/http"
	"strconv"

	"github.com/brienze1/lyrebird/internal/usecase"
)

// applyRewrite mutates out (the outbound request httputil.ReverseProxy is
// about to send) per rw. A nil Method/Path leaves that field unchanged.
// Headers merges by key: a present key mapped to a nil slice deletes that
// header, anything else sets/replaces it — never a wholesale replace of the
// whole header set, so a script only has to name the headers it actually
// wants to change.
func applyRewrite(out *http.Request, rw usecase.RewrittenRequest) {
	if rw.Method != nil {
		out.Method = *rw.Method
	}
	if rw.Path != nil {
		out.URL.Path = *rw.Path
		out.URL.RawPath = "" // let Path re-encode naturally rather than keep a stale raw form
	}
	applyHeaders(out.Header, rw.Headers)
	if rw.BodySet {
		out.Body = io.NopCloser(bytes.NewReader(rw.Body))
		out.ContentLength = int64(len(rw.Body))
		out.Header.Set("Content-Length", strconv.Itoa(len(rw.Body)))
	}
}

// applyTransform mirrors applyRewrite for the response side.
func applyTransform(resp *http.Response, tr usecase.TransformedResponse) {
	if tr.Status != nil {
		resp.StatusCode = *tr.Status
	}
	applyHeaders(resp.Header, tr.Headers)
	if tr.BodySet {
		resp.Body = io.NopCloser(bytes.NewReader(tr.Body))
		resp.ContentLength = int64(len(tr.Body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(tr.Body)))
	}
}

func applyHeaders(dst http.Header, changes map[string][]string) {
	for k, v := range changes {
		if v == nil {
			dst.Del(k)
			continue
		}
		dst[http.CanonicalHeaderKey(k)] = v
	}
}
