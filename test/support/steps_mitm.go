package support

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/usecase"
)

// mitmState drives mitm.feature's raw-socket CONNECT/TLS-handshake
// scenarios against the shared appState's real data-plane listener — proving
// genuine TLS termination (not blind tunneling) the same way steps_mcp.go
// proves the real MCP wiring rather than bypassing it.
type mitmState struct {
	s *appState

	lastRawConnectStatus int

	fetchedCACerts   [][]byte
	lastMCPCACertPEM []byte

	tunnelConn *tls.Conn
	tunnelHost string

	lastTunnelResp     *http.Response
	lastTunnelRespBody []byte

	drivenSurface []byte
}

// syncBuffer is a concurrency-safe io.Writer used as a booted app's slog
// destination (instead of io.Discard) so mitm.feature can assert the CA
// private key never appears in a log line — a plain bytes.Buffer would race
// against Lyrebird's own concurrent request-handling goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// bufConn wraps a raw net.Conn with the bufio.Reader that already consumed
// bytes off it while parsing an HTTP response (e.g. the CONNECT tunnel's
// "200 Connection Established"), so a later tls.Client wrapping this Conn
// reads any bytes the bufio.Reader had already buffered before falling
// through to the raw connection — otherwise those bytes would be silently
// lost, corrupting the TLS handshake that follows.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// dataPlaneConnect dials the shared appState's data-plane listener, sends a
// raw CONNECT request for target (host:port), and reads back the response
// line — without assuming it succeeded, so callers can assert on the status
// themselves (MITM-disabled scenarios expect a non-200 status here).
func dataPlaneConnect(ctx context.Context, s *appState, target string) (net.Conn, *http.Response, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", s.app.DataAddr())
	if err != nil {
		return nil, nil, fmt.Errorf("dial data plane: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodConnect, "http://"+target, nil)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("build CONNECT request: %w", err)
	}
	req.Host = target
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("write CONNECT request: %w", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	// A successful CONNECT response carries no real HTTP body (the bytes
	// that follow are the tunneled protocol, not response framing) and this
	// Close only marks the parsed Body done — it does not touch conn, which
	// callers go on to use for the TLS handshake / raw socket work below.
	_ = resp.Body.Close()
	return &bufConn{Conn: conn, r: br}, resp, nil
}

// fetchCACertPEM fetches the CA certificate over the real Admin REST route —
// used both by the explicit "I fetch the CA certificate over REST" step and
// internally to build the trust pool for a simulated client's TLS handshake.
func fetchCACertPEM(ctx context.Context, s *appState) ([]byte, error) {
	url := fmt.Sprintf("http://%s/__lyrebird/mitm/ca-cert", s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build ca-cert request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET /__lyrebird/mitm/ca-cert: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /__lyrebird/mitm/ca-cert status = %d, want 200", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// generateSelfSignedCA produces a throwaway ECDSA CA cert+key PEM pair for
// the "stable CA via mounted files" fixture — independent of the CA
// implementation under test, so this fixture keeps working unchanged
// regardless of how internal/infra/mitmca ends up generating its own CAs.
func generateSelfSignedCA() (certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "lyrebird-bdd-fixture-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("create CA certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal CA key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func (m *mitmState) aStableMITMCAIsMountedFromGeneratedFiles() error {
	certPEM, keyPEM, err := generateSelfSignedCA()
	if err != nil {
		return fmt.Errorf("generate fixture CA: %w", err)
	}
	certPath := filepath.Join(m.s.dataDir, "mitm-ca-cert.pem")
	keyPath := filepath.Join(m.s.dataDir, "mitm-ca-key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return fmt.Errorf("write fixture CA cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write fixture CA key: %w", err)
	}
	m.s.mitmCACertFile = certPath
	m.s.mitmCAKeyFile = keyPath
	return nil
}

func (m *mitmState) iSendARawCONNECTRequestToTheDataPlaneFor(ctx context.Context, target string) error {
	conn, resp, err := dataPlaneConnect(ctx, m.s, target) //nolint:bodyclose // dataPlaneConnect already closes resp.Body before returning
	if err != nil {
		return err
	}
	m.lastRawConnectStatus = resp.StatusCode
	return conn.Close()
}

func (m *mitmState) theRawCONNECTResponseStatusIs(want int) error {
	if m.lastRawConnectStatus != want {
		return fmt.Errorf("raw CONNECT response status = %d, want %d", m.lastRawConnectStatus, want)
	}
	return nil
}

// iCompleteACONNECTTunnelAndATLSHandshakeTrustingOnlyLyrebirdsCA fetches
// Lyrebird's own CA cert to build a trust pool containing ONLY that CA (not
// the system roots), then dials the data plane, CONNECTs to target, and
// performs a real TLS handshake with SNI sni over the raw tunnel. The
// handshake only succeeds if Lyrebird terminated TLS with a leaf certificate
// actually signed by its own CA — proving genuine interception, not blind
// byte-forwarding (data-model.md's MITM CA design). target need not be
// reachable: Lyrebird's CONNECT handler terminates TLS locally and never
// dials target until (and unless) a request inside the tunnel actually falls
// through to the proxy path.
func (m *mitmState) iCompleteACONNECTTunnelAndATLSHandshakeTrustingOnlyLyrebirdsCA(ctx context.Context, target, sni string) error {
	caPEM, err := fetchCACertPEM(ctx, m.s)
	if err != nil {
		return fmt.Errorf("fetch CA cert to build trust pool: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("failed to parse fetched CA cert PEM into a trust pool")
	}

	conn, resp, err := dataPlaneConnect(ctx, m.s, target) //nolint:bodyclose // dataPlaneConnect already closes resp.Body before returning
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return fmt.Errorf("CONNECT response status = %d, want 200", resp.StatusCode)
	}

	tlsConn := tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: sni, MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return fmt.Errorf("TLS handshake over MITM tunnel: %w", err)
	}
	m.tunnelConn = tlsConn
	m.tunnelHost = sni
	return nil
}

func (m *mitmState) theMITMTLSHandshakeSucceeds() error {
	if m.tunnelConn == nil {
		return fmt.Errorf("no established MITM tunnel connection")
	}
	return nil
}

func (m *mitmState) iSendAGETRequestForOverTheEstablishedMITMTunnel(ctx context.Context, path string) error {
	if m.tunnelConn == nil {
		return fmt.Errorf("no established MITM tunnel connection")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("build tunneled GET request: %w", err)
	}
	req.Host = m.tunnelHost
	req.Header.Set("Connection", "close")
	if err := req.Write(m.tunnelConn); err != nil {
		return fmt.Errorf("write tunneled GET request: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(m.tunnelConn), req)
	if err != nil {
		return fmt.Errorf("read tunneled response: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read tunneled response body: %w", err)
	}
	m.lastTunnelResp, m.lastTunnelRespBody = resp, body
	return nil
}

func (m *mitmState) theMITMTunnelResponseStatusIs(want int) error {
	if m.lastTunnelResp == nil {
		return fmt.Errorf("no MITM tunnel response recorded")
	}
	if m.lastTunnelResp.StatusCode != want {
		return fmt.Errorf("MITM tunnel response status = %d, want %d", m.lastTunnelResp.StatusCode, want)
	}
	return nil
}

func (m *mitmState) theMITMTunnelResponseBodyIs(want string) error {
	got := string(m.lastTunnelRespBody)
	if got != want {
		return fmt.Errorf("MITM tunnel response body = %q, want %q", got, want)
	}
	return nil
}

func (m *mitmState) theLastRecordedTrafficInTheDefaultPartitionHasDecision(ctx context.Context, want string) error {
	list, err := m.s.app.Store.ListTraffic(ctx, "default", usecase.TrafficFilter{})
	if err != nil {
		return fmt.Errorf("list traffic: %w", err)
	}
	if len(list) == 0 {
		return fmt.Errorf(`no traffic recorded in partition "default"`)
	}
	got := string(list[0].Decision)
	if got != want {
		return fmt.Errorf("recorded decision = %q, want %q", got, want)
	}
	return nil
}

func (m *mitmState) iFetchTheCACertificateOverREST(ctx context.Context) error {
	pemBytes, err := fetchCACertPEM(ctx, m.s)
	if err != nil {
		return err
	}
	m.fetchedCACerts = append(m.fetchedCACerts, pemBytes)
	return nil
}

func mitmMCPSession(ctx context.Context, s *appState) (*sdkmcp.ClientSession, error) {
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "lyrebird-bdd-mitm", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint: fmt.Sprintf("http://%s/mcp", s.app.ControlAddr()),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect MCP client: %w", err)
	}
	return session, nil
}

func (m *mitmState) iFetchTheCACertificateOverMCP(ctx context.Context) error {
	session, err := mitmMCPSession(ctx, m.s)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "get_mitm_ca_cert", Arguments: map[string]any{}})
	if err != nil {
		return fmt.Errorf("call get_mitm_ca_cert: %w", err)
	}
	if result.IsError {
		return fmt.Errorf("get_mitm_ca_cert tool error: %s", contentText(result))
	}
	m.lastMCPCACertPEM = []byte(contentText(result))
	return nil
}

func mustParseCertPEM(raw []byte) error {
	block, _ := pem.Decode(raw)
	if block == nil {
		return fmt.Errorf("not a valid PEM block")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return fmt.Errorf("parse certificate: %w", err)
	}
	return nil
}

func (m *mitmState) bothFetchedCACertificatesAreValidPEMAndIdentical() error {
	if len(m.fetchedCACerts) == 0 {
		return fmt.Errorf("no CA certificate fetched over REST yet")
	}
	restPEM := m.fetchedCACerts[len(m.fetchedCACerts)-1]
	if err := mustParseCertPEM(restPEM); err != nil {
		return fmt.Errorf("REST-fetched CA cert: %w", err)
	}
	if err := mustParseCertPEM(m.lastMCPCACertPEM); err != nil {
		return fmt.Errorf("MCP-fetched CA cert: %w", err)
	}
	if !bytes.Equal(bytes.TrimSpace(restPEM), bytes.TrimSpace(m.lastMCPCACertPEM)) {
		return fmt.Errorf("REST and MCP CA certificates differ")
	}
	return nil
}

func (m *mitmState) lastTwoFetched() ([]byte, []byte, error) {
	n := len(m.fetchedCACerts)
	if n < 2 {
		return nil, nil, fmt.Errorf("expected at least 2 fetched CA certificates, got %d", n)
	}
	return m.fetchedCACerts[n-2], m.fetchedCACerts[n-1], nil
}

func (m *mitmState) theLastTwoFetchedCACertificatesDiffer() error {
	a, b, err := m.lastTwoFetched()
	if err != nil {
		return err
	}
	if bytes.Equal(a, b) {
		return fmt.Errorf("expected two different CA certificates across boots, got identical PEM")
	}
	return nil
}

func (m *mitmState) theLastTwoFetchedCACertificatesAreIdentical() error {
	a, b, err := m.lastTwoFetched()
	if err != nil {
		return err
	}
	if !bytes.Equal(a, b) {
		return fmt.Errorf("expected the same CA certificate across boots, got different PEM")
	}
	return nil
}

func getRESTBodyIgnoringStatus(ctx context.Context, s *appState, path string) ([]byte, error) {
	url := fmt.Sprintf("http://%s%s", s.app.ControlAddr(), path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}

// iDriveEveryRegisteredRESTAndMCPEndpoint exercises a representative sample
// of every REST route and MCP tool this app registers (including the new
// ca_cert surface) and accumulates every response body/content into one
// buffer, so the CA-key-never-exposed assertion has a broad, real surface to
// check rather than just the one ca_cert endpoint.
func (m *mitmState) iDriveEveryRegisteredRESTAndMCPEndpoint(ctx context.Context) error {
	var all bytes.Buffer

	for _, path := range []string{"/__lyrebird/mitm/ca-cert", "/__lyrebird/healthz", "/__lyrebird/readyz", "/__lyrebird/traffic"} {
		body, _ := getRESTBodyIgnoringStatus(ctx, m.s, path)
		all.Write(body)
	}

	session, err := mitmMCPSession(ctx, m.s)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"get_mitm_ca_cert", map[string]any{}},
		{"list_traffic", map[string]any{}},
		{"inspect_requests", map[string]any{}},
		{"lyrebird_guide", map[string]any{}},
	} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: call.name, Arguments: call.args})
		if err != nil {
			return fmt.Errorf("call %s: %w", call.name, err)
		}
		all.WriteString(contentText(result))
		if result.StructuredContent != nil {
			raw, err := json.Marshal(result.StructuredContent)
			if err != nil {
				return fmt.Errorf("marshal %s structured content: %w", call.name, err)
			}
			all.Write(raw)
		}
	}

	m.drivenSurface = all.Bytes()
	return nil
}

func (m *mitmState) theCAPrivateKeyNeverAppearsInAnyDrivenResponseOrInTheCapturedLogs() error {
	if bytes.Contains(m.drivenSurface, []byte("PRIVATE KEY")) {
		return fmt.Errorf("CA private key marker found in driven REST/MCP responses: %s", m.drivenSurface)
	}
	if m.s.logCapture != nil && strings.Contains(m.s.logCapture.String(), "PRIVATE KEY") {
		return fmt.Errorf("CA private key marker found in captured log output")
	}
	return nil
}

// RegisterMITMSteps wires mitm.feature's steps against the shared appState
// s. Mock creation (matching GET path .../that responds .../with body ...)
// is deliberately NOT redeclared here — it reuses steps_mock.go's existing
// pattern, since godog matches step patterns across every file registered
// into one ScenarioContext.
func RegisterMITMSteps(sc *godog.ScenarioContext, s *appState) {
	m := &mitmState{s: s}

	sc.Step(`^a stable MITM CA is mounted from generated files$`, m.aStableMITMCAIsMountedFromGeneratedFiles)

	sc.Step(`^I send a raw CONNECT request to the data plane for "([^"]*)"$`, m.iSendARawCONNECTRequestToTheDataPlaneFor)
	sc.Step(`^the raw CONNECT response status is (\d+)$`, m.theRawCONNECTResponseStatusIs)

	sc.Step(`^I complete a CONNECT tunnel to "([^"]*)" and a TLS handshake with SNI "([^"]*)", trusting only Lyrebird's CA$`,
		m.iCompleteACONNECTTunnelAndATLSHandshakeTrustingOnlyLyrebirdsCA)
	sc.Step(`^the MITM TLS handshake succeeds$`, m.theMITMTLSHandshakeSucceeds)
	sc.Step(`^I send a GET request for "([^"]*)" over the established MITM tunnel$`, m.iSendAGETRequestForOverTheEstablishedMITMTunnel)
	sc.Step(`^the MITM tunnel response status is (\d+)$`, m.theMITMTunnelResponseStatusIs)
	sc.Step(`^the MITM tunnel response body is "([^"]*)"$`, m.theMITMTunnelResponseBodyIs)
	sc.Step(`^the last recorded traffic in the default partition has decision "([^"]*)"$`,
		m.theLastRecordedTrafficInTheDefaultPartitionHasDecision)

	sc.Step(`^I fetch the CA certificate over REST$`, m.iFetchTheCACertificateOverREST)
	sc.Step(`^I fetch the CA certificate over MCP$`, m.iFetchTheCACertificateOverMCP)
	sc.Step(`^both fetched CA certificates are valid PEM and identical$`, m.bothFetchedCACertificatesAreValidPEMAndIdentical)
	sc.Step(`^the last two fetched CA certificates differ$`, m.theLastTwoFetchedCACertificatesDiffer)
	sc.Step(`^the last two fetched CA certificates are identical$`, m.theLastTwoFetchedCACertificatesAreIdentical)

	sc.Step(`^I drive every registered REST and MCP endpoint$`, m.iDriveEveryRegisteredRESTAndMCPEndpoint)
	sc.Step(`^the CA private key never appears in any driven response or in the captured logs$`,
		m.theCAPrivateKeyNeverAppearsInAnyDrivenResponseOrInTheCapturedLogs)
}
