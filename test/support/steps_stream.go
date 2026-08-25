package support

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// streamState holds the per-scenario byte-stream clients, sharing the booted
// app + control plane through the common appState — the same shape
// steps_grpc.go uses for the gRPC plane.
type streamState struct {
	s *appState

	conns    []*standIn
	lastErr  error
	lastMock map[string]any

	// lastPromotedID is set by the promote step and asserted by the next one.
	lastPromotedID string
	promoteErr     error
}

// standIn is one connected (or refused) peripheral stand-in.
type standIn struct {
	conn   net.Conn
	reader *bufio.Reader
	reply  string
}

// frameReadTimeout bounds every "expect a frame" assertion. It is generous
// because it must not flake on a loaded CI box, and short enough that a
// genuinely silent endpoint fails a scenario in seconds rather than minutes.
const frameReadTimeout = 3 * time.Second

// silenceWindow is how long "receives nothing" waits before believing it.
// Shorter than frameReadTimeout because proving a negative costs this long on
// every such scenario.
const silenceWindow = 350 * time.Millisecond

func (g *streamState) enableStream() error {
	g.s.streamEnabled = true
	return nil
}

// ---------------------------------------------------------------- endpoints

func (g *streamState) endpointDelimitedByCRLF(name string) error {
	return g.createEndpoint(name, "default", map[string]any{"delimiter": "\r\n"}, nil, nil)
}

func (g *streamState) endpointInSpace(name, space string) error {
	return g.createEndpoint(name, space, map[string]any{"delimiter": "\r\n"}, nil, nil)
}

func (g *streamState) endpointWithSplit(name, sep string) error {
	return g.createEndpoint(name, "default", map[string]any{"delimiter": "\r\n"},
		map[string]any{"split": sep}, nil)
}

func (g *streamState) endpointWithCadence(name, interval string, frames *godog.DocString) error {
	var specs []string
	if err := json.Unmarshal([]byte(frames.Content), &specs); err != nil {
		return fmt.Errorf("parse cadence frames: %w", err)
	}
	structured := make([][]map[string]any, 0, len(specs))
	for _, spec := range specs {
		var parts []map[string]any
		if err := json.Unmarshal([]byte(spec), &parts); err != nil {
			return fmt.Errorf("parse cadence frame %q: %w", spec, err)
		}
		structured = append(structured, parts)
	}
	return g.createEndpoint(name, "default", map[string]any{"delimiter": "\r\n"}, nil, map[string]any{
		"interval": interval,
		"frames":   structured,
	})
}

// seededEndpoint writes a seed file rather than calling the API, so the
// scenario proves the seeded lifetime (reset-immune) rather than just the
// declaration.
func (g *streamState) seededEndpoint(name string) error {
	yaml := fmt.Sprintf("space: default\nendpoints:\n  - name: %s\n    framing: {delimiter: \"\\r\\n\"}\n", name)
	return os.WriteFile(filepath.Join(g.s.seedDir, "stream-seed.yaml"), []byte(yaml), 0o600)
}

func (g *streamState) createEndpoint(name, space string, framing, projection, cadence map[string]any) error {
	payload := map[string]any{"name": name, "framing": framing}
	if projection != nil {
		payload["projection"] = projection
	}
	if cadence != nil {
		payload["cadence"] = cadence
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := g.post("/__lyrebird/stream/endpoints", space, raw)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create endpoint %q status = %d: %s", name, resp.StatusCode, body)
	}
	return nil
}

// ---------------------------------------------------------------- mocks

func (g *streamState) streamMock(name, path string, body *godog.DocString) error {
	return g.createMock(name, path, "default", body.Content, nil, nil, nil)
}

func (g *streamState) streamMockInSpace(name, path, space string, body *godog.DocString) error {
	return g.createMock(name, path, space, body.Content, nil, nil, nil)
}

func (g *streamState) streamMockMatching(name, path, jsonPath, want string, body *godog.DocString) error {
	match := []map[string]any{{"jsonpath": jsonPath, "equals": want}}
	return g.createMock(name, path, "default", body.Content, match, nil, nil)
}

func (g *streamState) streamMockWithScript(name, path string, src *godog.DocString) error {
	return g.createMock(name, path, "default", "", nil, nil, map[string]any{"respond_src": src.Content})
}

func (g *streamState) streamMockFaulting(name, path, kind string) error {
	return g.createMock(name, path, "default", "", nil, map[string]any{"kind": kind}, nil)
}

func (g *streamState) streamMockFaultingWithDelay(name, path, kind, delayMS string) error {
	fault := map[string]any{"kind": kind, "delay_ms": mustAtoi(delayMS)}
	return g.createMock(name, path, "default", "", nil, fault, nil)
}

// projectsAt re-creates the last mock with a rule-level projection override —
// the FR-006 case where a rule reads the same bytes differently from every
// other rule on its endpoint.
func (g *streamState) projectsAt(name, field string, offset, length int, as string) error {
	if g.lastMock == nil {
		return fmt.Errorf("no stream mock has been created yet")
	}
	g.lastMock["projection"] = map[string]any{
		"at": []map[string]any{{"name": field, "offset": offset, "length": length, "as": as}},
	}
	raw, err := json.Marshal(g.lastMock)
	if err != nil {
		return err
	}
	resp, err := g.post("/__lyrebird/mocks", "default", raw)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("re-create mock %q with projection: status %d: %s", name, resp.StatusCode, body)
	}
	return nil
}

func (g *streamState) createMock(name, path, space, body string, match []map[string]any, fault, script map[string]any) error {
	matchBlock := map[string]any{"method": "FRAME", "path": path}
	if match != nil {
		matchBlock["body"] = match
	}
	action := map[string]any{}
	// domain.Action allows exactly one variant, so a fault rule carries no
	// respond body — the same shape the HTTP plane's faults have.
	if fault != nil {
		action["fault"] = fault
	} else {
		action["respond"] = map[string]any{"body": body}
	}

	payload := map[string]any{"name": name, "match": matchBlock, "action": action}
	if script != nil {
		payload["script"] = script
	}
	g.lastMock = payload

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := g.post("/__lyrebird/mocks", space, raw)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create mock %q status = %d: %s", name, resp.StatusCode, b)
	}
	return nil
}

// proxyMockIsRejected proves FR-025 at the surface an author actually uses:
// the refusal happens when the rule is written, not when a frame arrives.
func (g *streamState) proxyMockIsRejected(path string) error {
	payload := map[string]any{
		"name":   "proxy-on-stream",
		"match":  map[string]any{"method": "FRAME", "path": path},
		"action": map[string]any{"proxy": map[string]any{}},
	}
	raw, _ := json.Marshal(payload)
	resp, err := g.post("/__lyrebird/mocks", "default", raw)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 400 {
		return fmt.Errorf("creating a FRAME proxy mock returned %d, want a client error", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------- stand-ins

func (g *streamState) standInConnects(endpoint string) error {
	return g.connect(endpoint, "")
}

func (g *streamState) standInConnectsInSpace(endpoint, space string) error {
	return g.connect(endpoint, space)
}

func (g *streamState) secondStandInConnects(endpoint string) error {
	return g.connect(endpoint, "")
}

func (g *streamState) connect(endpoint, space string) error {
	addr := g.s.app.StreamAddr()
	if addr == "" {
		return fmt.Errorf("the byte-stream data plane is not listening")
	}
	dialer := &net.Dialer{Timeout: frameReadTimeout}
	conn, err := dialer.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial stream plane: %w", err)
	}
	line := "LYREBIRD/1 " + endpoint
	if space != "" {
		line += " space=" + space
	}
	if _, err := conn.Write([]byte(line + "\r\n")); err != nil {
		return fmt.Errorf("write handshake: %w", err)
	}
	si := &standIn{conn: conn, reader: bufio.NewReader(conn)}

	if err := conn.SetReadDeadline(time.Now().Add(frameReadTimeout)); err != nil {
		return err
	}
	reply, err := si.reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read handshake reply: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	si.reply = strings.TrimRight(reply, "\r\n")

	g.conns = append(g.conns, si)
	g.s.preShutdownCleanup = append(g.s.preShutdownCleanup, func() { _ = conn.Close() })
	return nil
}

func (g *streamState) handshakeAccepted() error {
	si, err := g.current()
	if err != nil {
		return err
	}
	if si.reply != "OK" {
		return fmt.Errorf("handshake reply = %q, want OK", si.reply)
	}
	return nil
}

func (g *streamState) handshakeRefusedWith(want string) error {
	si, err := g.current()
	if err != nil {
		return err
	}
	if si.reply != want {
		return fmt.Errorf("handshake reply = %q, want %q", si.reply, want)
	}
	return nil
}

func (g *streamState) sendFrame(frame string) error {
	si, err := g.current()
	if err != nil {
		return err
	}
	if _, err := si.conn.Write([]byte(frame + "\r\n")); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

// sendUnterminated writes bytes that never complete a frame, so the reader's
// cap-and-resynchronise path (FR-034) is what the next frame depends on.
func (g *streamState) sendUnterminated(n int) error {
	si, err := g.current()
	if err != nil {
		return err
	}
	if _, err := si.conn.Write(bytes.Repeat([]byte("x"), n)); err != nil {
		return fmt.Errorf("write unterminated bytes: %w", err)
	}
	return nil
}

func (g *streamState) receivesFrame(want string) error {
	got, err := g.readFrame()
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("received frame %q, want %q", got, want)
	}
	return nil
}

func (g *streamState) receivesFrameAfter(want, minimum string) error {
	d, err := time.ParseDuration(minimum)
	if err != nil {
		return err
	}
	start := time.Now()
	got, err := g.readFrame()
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("received frame %q, want %q", got, want)
	}
	if elapsed := time.Since(start); elapsed < d {
		return fmt.Errorf("frame arrived after %v, want at least %v", elapsed, d)
	}
	return nil
}

// receivesUnterminated reads exactly the declared bytes and then asserts that
// NO framing terminator follows — which is the corruption a malformed fault
// produces (FR-016).
func (g *streamState) receivesUnterminated(want string) error {
	si, err := g.current()
	if err != nil {
		return err
	}
	if err := si.conn.SetReadDeadline(time.Now().Add(frameReadTimeout)); err != nil {
		return err
	}
	buf := make([]byte, len(want))
	if _, err := io.ReadFull(si.reader, buf); err != nil {
		return fmt.Errorf("read unterminated answer: %w", err)
	}
	if string(buf) != want {
		return fmt.Errorf("received %q, want %q", buf, want)
	}
	if err := si.conn.SetReadDeadline(time.Now().Add(silenceWindow)); err != nil {
		return err
	}
	extra := make([]byte, 2)
	n, err := si.reader.Read(extra)
	if err == nil && n > 0 && string(extra[:n]) == "\r\n" {
		return fmt.Errorf("answer carried its framing terminator, want it omitted")
	}
	_ = si.conn.SetReadDeadline(time.Time{})
	return nil
}

func (g *streamState) receivesNothing() error {
	si, err := g.current()
	if err != nil {
		return err
	}
	if err := si.conn.SetReadDeadline(time.Now().Add(silenceWindow)); err != nil {
		return err
	}
	defer func() { _ = si.conn.SetReadDeadline(time.Time{}) }()

	buf := make([]byte, 64)
	n, readErr := si.reader.Read(buf)
	if n > 0 {
		return fmt.Errorf("expected silence, received %q", buf[:n])
	}
	var netErr net.Error
	if errors.As(readErr, &netErr) && netErr.Timeout() {
		return nil
	}
	if errors.Is(readErr, io.EOF) {
		return fmt.Errorf("expected silence with the connection usable, but it closed")
	}
	return nil
}

func (g *streamState) observesConnectionClosing() error {
	si, err := g.current()
	if err != nil {
		return err
	}
	if err := si.conn.SetReadDeadline(time.Now().Add(frameReadTimeout)); err != nil {
		return err
	}
	buf := make([]byte, 64)
	for {
		n, readErr := si.reader.Read(buf)
		if readErr != nil {
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, net.ErrClosed) {
				return nil
			}
			var netErr net.Error
			if errors.As(readErr, &netErr) && netErr.Timeout() {
				return fmt.Errorf("connection was still open after %v, want it closed", frameReadTimeout)
			}
			// A peer-reset shows up as a syscall error rather than EOF; both
			// are the peripheral going away as far as a stand-in is
			// concerned.
			return nil
		}
		if n == 0 {
			return nil
		}
	}
}

func (g *streamState) readFrame() (string, error) {
	si, err := g.current()
	if err != nil {
		return "", err
	}
	if err := si.conn.SetReadDeadline(time.Now().Add(frameReadTimeout)); err != nil {
		return "", err
	}
	defer func() { _ = si.conn.SetReadDeadline(time.Time{}) }()

	line, err := si.reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read frame: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (g *streamState) current() (*standIn, error) {
	if len(g.conns) == 0 {
		return nil, fmt.Errorf("no stand-in has connected yet")
	}
	return g.conns[len(g.conns)-1], nil
}

// ---------------------------------------------------------------- control

func (g *streamState) injectFrame(spec, endpoint string) error {
	raw, err := json.Marshal(map[string]any{"endpoint": endpoint, "frame": unescape(spec)})
	if err != nil {
		return err
	}
	resp, err := g.post("/__lyrebird/stream/emit", "default", raw)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		g.lastErr = fmt.Errorf("emit status %d", resp.StatusCode)
		return nil
	}
	g.lastErr = nil
	return nil
}

func (g *streamState) injectionRejected() error {
	if g.lastErr == nil {
		return fmt.Errorf("injecting into an endpoint with no stand-in succeeded, want an explanatory error")
	}
	return nil
}

func (g *streamState) trafficHasEntryWithMethod(path, method string) error {
	entries, err := g.traffic(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e["method"] == method {
			return nil
		}
	}
	return fmt.Errorf("no traffic entry for %q with method %q (got %d entries)", path, method, len(entries))
}

func (g *streamState) trafficHasEntryWithDecision(path, decision string) error {
	entries, err := g.traffic(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e["decision"] == decision {
			return nil
		}
	}
	return fmt.Errorf("no traffic entry for %q with decision %q (got %d entries)", path, decision, len(entries))
}

func (g *streamState) promoteRecorded(method, path string) error {
	entries, err := g.traffic(path)
	if err != nil {
		return err
	}
	var id string
	for _, e := range entries {
		if e["method"] == method {
			id, _ = e["id"].(string)
			break
		}
	}
	if id == "" {
		return fmt.Errorf("no recorded %s frame for %q to promote", method, path)
	}
	resp, err := g.post("/__lyrebird/traffic/"+id+"/promote", "default", []byte(`{}`))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		g.promoteErr = fmt.Errorf("promote status %d: %s", resp.StatusCode, body)
		return nil
	}
	var mock map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&mock); err != nil {
		return err
	}
	g.lastPromotedID, _ = mock["id"].(string)
	return nil
}

func (g *streamState) promotedMockExists() error {
	if g.promoteErr != nil {
		return g.promoteErr
	}
	if g.lastPromotedID == "" {
		return fmt.Errorf("no mock was promoted")
	}
	return nil
}

func (g *streamState) exportWarnsAboutCapturedTraffic() error {
	raw, err := g.export()
	if err != nil {
		return err
	}
	if !strings.Contains(raw, "promoted from recorded traffic") {
		return fmt.Errorf("export carries a promoted mock but no captured-traffic warning:\n%s", raw)
	}
	return nil
}

func (g *streamState) exportContainsEndpoint(name string) error {
	raw, err := g.export()
	if err != nil {
		return err
	}
	if !strings.Contains(raw, "endpoints:") || !strings.Contains(raw, "name: "+name) {
		return fmt.Errorf("export does not render endpoint %q:\n%s", name, raw)
	}
	return nil
}

func (g *streamState) endpointListingReportsOccupied(name string) error {
	resp, err := g.get("/__lyrebird/stream/endpoints", "default")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return err
	}
	for _, e := range list {
		if e["name"] == name {
			if occupied, _ := e["occupied"].(bool); occupied {
				return nil
			}
			return fmt.Errorf("endpoint %q is listed as unoccupied, want occupied", name)
		}
	}
	return fmt.Errorf("endpoint %q not in the listing", name)
}

func (g *streamState) export() (string, error) {
	resp, err := g.get("/__lyrebird/export", "default")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return string(raw), err
}

func (g *streamState) traffic(path string) ([]map[string]any, error) {
	resp, err := g.get("/__lyrebird/traffic?path_prefix="+path, "default")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var entries []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode traffic listing: %w", err)
	}
	return entries, nil
}

// ---------------------------------------------------------------- plumbing

func (g *streamState) post(path, space string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+g.s.app.ControlAddr()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if space != "" {
		req.Header.Set("X-Lyrebird-Space", space)
	}
	return http.DefaultClient.Do(req)
}

func (g *streamState) get(path, space string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://"+g.s.app.ControlAddr()+path, nil)
	if err != nil {
		return nil, err
	}
	if space != "" {
		req.Header.Set("X-Lyrebird-Space", space)
	}
	return http.DefaultClient.Do(req)
}

// unescape turns a Gherkin-escaped inline JSON spec back into JSON. A frame
// spec is itself JSON, so writing one inline in a step means escaping its
// quotes; this is the one place that is undone.
func unescape(s string) string {
	return strings.ReplaceAll(s, `\"`, `"`)
}

func mustAtoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// RegisterStreamSteps wires the byte-stream data plane's scenarios.
func RegisterStreamSteps(sc *godog.ScenarioContext, s *appState) {
	g := &streamState{s: s}

	sc.Step(`^the byte-stream data plane is enabled$`, g.enableStream)

	sc.Step(`^a stream endpoint "([^"]*)" delimited by CRLF$`, g.endpointDelimitedByCRLF)
	sc.Step(`^a stream endpoint "([^"]*)" delimited by CRLF in space "([^"]*)"$`, g.endpointInSpace)
	sc.Step(`^a stream endpoint "([^"]*)" delimited by CRLF splitting on "([^"]*)"$`, g.endpointWithSplit)
	sc.Step(`^a stream endpoint "([^"]*)" delimited by CRLF emitting every "([^"]*)":$`, g.endpointWithCadence)
	sc.Step(`^a seeded stream endpoint "([^"]*)" delimited by CRLF$`, g.seededEndpoint)

	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" responding with:$`, g.streamMock)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" in space "([^"]*)" responding with:$`, g.streamMockInSpace)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" matching "([^"]*)" equal to "([^"]*)" responding with:$`, g.streamMockMatching)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" responding with the script:$`, g.streamMockWithScript)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" faulting with "([^"]*)"$`, g.streamMockFaulting)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" faulting with "([^"]*)" of "([^"]*)" ms$`, g.streamMockFaultingWithDelay)
	sc.Step(`^the stream mock "([^"]*)" projects "([^"]*)" at offset (\d+) length (\d+) as "([^"]*)"$`, g.projectsAt)
	sc.Step(`^creating a stream mock for "([^"]*)" with a proxy action is rejected$`, g.proxyMockIsRejected)

	sc.Step(`^a stand-in connects to endpoint "([^"]*)"$`, g.standInConnects)
	sc.Step(`^a stand-in connects to endpoint "([^"]*)" in space "([^"]*)"$`, g.standInConnectsInSpace)
	sc.Step(`^a second stand-in connects to endpoint "([^"]*)"$`, g.secondStandInConnects)
	sc.Step(`^the handshake is accepted$`, g.handshakeAccepted)
	sc.Step(`^the handshake is refused with "([^"]*)"$`, g.handshakeRefusedWith)
	sc.Step(`^the second handshake is refused with "([^"]*)"$`, g.handshakeRefusedWith)

	sc.Step(`^the stand-in sends the frame "([^"]*)"$`, g.sendFrame)
	sc.Step(`^the stand-in sends "(\d+)" unterminated bytes$`, g.sendUnterminated)
	sc.Step(`^the stand-in receives the frame "([^"]*)"$`, g.receivesFrame)
	sc.Step(`^the stand-in receives the frame "([^"]*)" after at least "([^"]*)"$`, g.receivesFrameAfter)
	sc.Step(`^the stand-in receives the unterminated bytes "([^"]*)"$`, g.receivesUnterminated)
	sc.Step(`^the stand-in receives nothing$`, g.receivesNothing)
	sc.Step(`^the stand-in observes the connection closing$`, g.observesConnectionClosing)

	sc.Step(`^I inject the frame "(.*)" into endpoint "([^"]*)"$`, g.injectFrame)
	sc.Step(`^the injection is rejected$`, g.injectionRejected)
	sc.Step(`^the traffic log has an entry for "([^"]*)" with method "([^"]*)"$`, g.trafficHasEntryWithMethod)
	sc.Step(`^the traffic log has an entry for "([^"]*)" with decision "([^"]*)"$`, g.trafficHasEntryWithDecision)
	sc.Step(`^I promote the recorded "([^"]*)" frame for "([^"]*)"$`, g.promoteRecorded)
	sc.Step(`^the promoted mock exists$`, g.promotedMockExists)
	sc.Step(`^the exported config warns about captured traffic$`, g.exportWarnsAboutCapturedTraffic)
	sc.Step(`^the exported config contains the endpoint "([^"]*)"$`, g.exportContainsEndpoint)
	sc.Step(`^the endpoint listing reports "([^"]*)" as occupied$`, g.endpointListingReportsOccupied)
}
