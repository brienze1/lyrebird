package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// mitmCA is the subset of *mitmca.CA's behavior Handler depends on, named at
// the point of use per this codebase's convention (see upstreamLister,
// trafficRecorder above).
type mitmCA interface {
	LeafCertFor(sni string) (*tls.Certificate, error)
}

// serveConnect handles a CONNECT request: the transparent forward-proxy/MITM
// entry point (T054). MITM disabled (h.mitmCA nil) rejects with 501 before
// ever hijacking the connection — Principle V, and what keeps every other
// code path provably unchanged when the feature is off. Enabled, it
// terminates TLS with a leaf certificate signed by Lyrebird's own CA
// (data-model.md's MITM Certificate Authority), reads exactly one plaintext
// HTTP request off the tunnel, and runs it through serveOne — the identical
// match/mock/fault/proxy/record pipeline a plain reverse-proxied request
// goes through. No keep-alive/pipelining within one tunnel (explicit scope
// cut): a real client's HTTP stack transparently opens a fresh CONNECT for
// its next request.
func (h *Handler) serveConnect(w http.ResponseWriter, r *http.Request, partition string) {
	if h.mitmCA == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte("mitm: forward-proxy/CONNECT is not enabled"))
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		h.log.Warn("proxy: mitm hijack failed", "err", err)
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := rw.Flush(); err != nil {
		return
	}

	tlsConn := tls.Server(&hijackedConn{Conn: conn, r: rw.Reader}, &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return h.mitmCA.LeafCertFor(hello.ServerName)
		},
		NextProtos: []string{"http/1.1"},
		MinVersion: tls.VersionTLS12,
	})
	defer func() { _ = tlsConn.Close() }()

	if err := tlsConn.HandshakeContext(r.Context()); err != nil {
		h.log.Warn("proxy: mitm TLS handshake failed", "err", err)
		return
	}

	innerReq, err := http.ReadRequest(bufio.NewReader(tlsConn))
	if err != nil {
		// Tunnel closed without a request, or a non-HTTP client — nothing to
		// serve or record.
		return
	}
	innerReq = innerReq.WithContext(r.Context())

	forwardTo := &url.URL{Scheme: "https", Host: r.Host}
	crw := newConnResponseWriter(tlsConn)
	h.serveOne(crw, innerReq, partition, forwardTo)
	_ = crw.close()
}

// hijackedConn overrides net.Conn's Read with r (the bufio.Reader hijacking
// already produced) so bytes the client sent immediately after the CONNECT
// headers — e.g. the start of its TLS ClientHello, if pipelined — aren't
// silently lost by reading the raw conn directly instead. Writes go straight
// to the underlying conn.
type hijackedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *hijackedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// connResponseWriter is an http.ResponseWriter that streams directly onto a
// hijacked connection using chunked transfer-encoding — necessary because,
// past the CONNECT hijack, there is no *http.Server left to frame the
// response for us. Chunked (rather than buffering the whole body to compute
// a Content-Length) keeps large proxied response bodies streaming instead of
// being held in memory in full.
type connResponseWriter struct {
	conn        net.Conn
	header      http.Header
	status      int
	wroteHeader bool
	chunked     io.WriteCloser
}

func newConnResponseWriter(conn net.Conn) *connResponseWriter {
	return &connResponseWriter{conn: conn, header: make(http.Header)}
}

func (w *connResponseWriter) Header() http.Header { return w.header }

func (w *connResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status

	w.header.Del("Content-Length")
	w.header.Set("Transfer-Encoding", "chunked")
	w.header.Set("Connection", "close")

	_, _ = fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	_ = w.header.Write(w.conn)
	_, _ = w.conn.Write([]byte("\r\n"))
	w.chunked = httputil.NewChunkedWriter(w.conn)
}

func (w *connResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.chunked.Write(p)
}

// close finalizes the chunked body (the terminating zero-length chunk).
// Must be called exactly once, after the handler invoked via serveOne has
// finished writing — flushes headers first if the handler never wrote
// anything at all (an empty 200 body).
func (w *connResponseWriter) close() error {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	// chunkedWriter.Close writes only the terminating "0\r\n" chunk marker —
	// per its own doc comment, the final CRLF ending an (empty) trailer
	// section is deliberately the caller's responsibility to write.
	// Omitting it leaves the client's chunked reader blocked expecting a
	// trailer line, surfacing as "unexpected EOF reading trailer" once the
	// connection then closes.
	if err := w.chunked.Close(); err != nil {
		return err
	}
	_, err := w.conn.Write([]byte("\r\n"))
	return err
}
