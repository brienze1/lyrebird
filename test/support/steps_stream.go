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
	"gopkg.in/yaml.v3"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
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
	body := fmt.Sprintf("space: default\nendpoints:\n  - name: %s\n    framing: {delimiter: \"\\r\\n\"}\n", name)
	return os.WriteFile(filepath.Join(g.s.seedDir, "stream-seed.yaml"), []byte(body), 0o600)
}

// seededEndpointWithCadence is seededEndpoint's cadence-bearing sibling: a
// seeded endpoint (protected from a space reset's DeleteEndpointsByPartition,
// unlike an ephemeral one) whose declared cadence a WI-02 cadence-override
// mock can then take over — the shape cb5/gps actually seeds in
// config/cb5-local-stack.yaml, needed here so a reset-survival scenario can
// prove the endpoint itself, and its connection, survive what the override
// clearing does NOT survive.
func (g *streamState) seededEndpointWithCadence(name, interval string, frames *godog.DocString) error {
	var specs []string
	if err := json.Unmarshal([]byte(frames.Content), &specs); err != nil {
		return fmt.Errorf("parse cadence frames: %w", err)
	}
	cadenceFrames := make([][]dto.FramePartDTO, 0, len(specs))
	for _, spec := range specs {
		var parts []dto.FramePartDTO
		if err := json.Unmarshal([]byte(spec), &parts); err != nil {
			return fmt.Errorf("parse cadence frame %q: %w", spec, err)
		}
		cadenceFrames = append(cadenceFrames, parts)
	}
	file := struct {
		Space     string            `yaml:"space"`
		Endpoints []dto.EndpointDTO `yaml:"endpoints"`
	}{
		Space: "default",
		Endpoints: []dto.EndpointDTO{{
			Name:    name,
			Framing: dto.FramingDTO{Delimiter: "\r\n"},
			Cadence: &dto.CadenceDTO{Interval: interval, Frames: cadenceFrames},
		}},
	}
	raw, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("marshal seeded cadence endpoint: %w", err)
	}
	return os.WriteFile(filepath.Join(g.s.seedDir, "stream-cadence-seed.yaml"), raw, 0o600)
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

// streamMockOverridingCadence creates a runtime cadence-override mock
// (WI-02): priority 100 so it outranks the endpoint's own seeded/declared
// cadence at the usual seed priority, action.cadence.frames carrying ONE
// frame spec — the consumer wire shape WI-03's steps and journey POST
// against. body is the same declarative frame-spec JSON a mock's respond
// body already accepts.
func (g *streamState) streamMockOverridingCadence(name, path string, body *godog.DocString) error {
	payload := map[string]any{
		"name":     name,
		"priority": 100,
		"match":    map[string]any{"method": "FRAME", "path": path},
		"action":   map[string]any{"cadence": map[string]any{"frames": []string{body.Content}}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := g.post("/__lyrebird/mocks", "default", raw)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create cadence-override mock %q status = %d: %s", name, resp.StatusCode, b)
	}
	return nil
}

// deleteMockNamed looks the mock up by name (an ephemeral mock's server-
// assigned id is never surfaced to a scenario) via GET /__lyrebird/mocks,
// then DELETEs it — the revert path WI-02's Story names alongside
// /__lyrebird/reset for restoring a cadence-override endpoint's seed.
//
// Both requests are built inline rather than through g.get/g.post: g.get's
// only existing callers all pass "default" too, and a fifth identical call
// tips golangci-lint's unparam check into flagging the parameter as always
// the same value — this stays a self-contained two-request helper instead of
// changing a shared helper's signature for a WI that isn't about it.
func (g *streamState) deleteMockNamed(name string) error {
	getReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://"+g.s.app.ControlAddr()+"/__lyrebird/mocks", nil)
	if err != nil {
		return err
	}
	getReq.Header.Set("X-Lyrebird-Space", "default")
	resp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var mocks []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&mocks); err != nil {
		return fmt.Errorf("decode mock listing: %w", err)
	}
	var id string
	for _, m := range mocks {
		if m["name"] == name {
			id, _ = m["id"].(string)
			break
		}
	}
	if id == "" {
		return fmt.Errorf("no mock named %q found to delete", name)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		"http://"+g.s.app.ControlAddr()+"/__lyrebird/mocks/"+id, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Lyrebird-Space", "default")
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = delResp.Body.Close() }()
	if delResp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(delResp.Body)
		return fmt.Errorf("delete mock %q (id %s) status = %d: %s", name, id, delResp.StatusCode, b)
	}
	return nil
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

// maxEventualFrames bounds receivesFrameEventually: a stand-in sharing its
// connection with a continuously-ticking cadence cannot assume the frame it
// wants is the very NEXT one — a cadence tick may legitimately interleave
// ahead of it — so this discards non-matching frames instead of failing on
// the first mismatch, up to a generous bound so a genuinely missing frame
// still fails loudly rather than hanging.
const maxEventualFrames = 500

// receivesFrameEventually reads frames from the current stand-in, discarding
// any that are not want, until want is seen or maxEventualFrames have been
// read with no match. This is the general shape a stand-in needs whenever a
// request/response exchange and an unrelated cadence share one connection —
// exactly cb5/gps's real situation (CB5-11 correction round 1): the u-blox
// handshake's answer and the position cadence's own ticks both cross the
// same wire, in no guaranteed relative order.
func (g *streamState) receivesFrameEventually(want string) error {
	for i := 0; i < maxEventualFrames; i++ {
		got, err := g.readFrame()
		if err != nil {
			return err
		}
		if got == want {
			return nil
		}
	}
	return fmt.Errorf("did not see the frame %q within %d frames", want, maxEventualFrames)
}

// standInDisconnects closes the current stand-in's own end of the
// connection — the client-initiated mirror of observesConnectionClosing,
// which only ever observes a SERVER-initiated close. Needed to prove a
// FRESH connection (opened after some other event, e.g. a runtime mock
// change) behaves correctly, without waiting on a reset or a fault to tear
// the old one down.
func (g *streamState) standInDisconnects() error {
	si, err := g.current()
	if err != nil {
		return err
	}
	return si.conn.Close()
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
	sc.Step(`^a seeded stream endpoint "([^"]*)" delimited by CRLF emitting every "([^"]*)":$`, g.seededEndpointWithCadence)

	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" responding with:$`, g.streamMock)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" in space "([^"]*)" responding with:$`, g.streamMockInSpace)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" matching "([^"]*)" equal to "([^"]*)" responding with:$`, g.streamMockMatching)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" responding with the script:$`, g.streamMockWithScript)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" faulting with "([^"]*)"$`, g.streamMockFaulting)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" faulting with "([^"]*)" of "([^"]*)" ms$`, g.streamMockFaultingWithDelay)
	sc.Step(`^a stream mock "([^"]*)" for "([^"]*)" overriding the cadence with:$`, g.streamMockOverridingCadence)
	sc.Step(`^the stream mock "([^"]*)" projects "([^"]*)" at offset (\d+) length (\d+) as "([^"]*)"$`, g.projectsAt)
	sc.Step(`^creating a stream mock for "([^"]*)" with a proxy action is rejected$`, g.proxyMockIsRejected)
	sc.Step(`^I delete the mock "([^"]*)"$`, g.deleteMockNamed)

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
	sc.Step(`^the stand-in eventually receives the frame "([^"]*)"$`, g.receivesFrameEventually)
	sc.Step(`^the stand-in receives the unterminated bytes "([^"]*)"$`, g.receivesUnterminated)
	sc.Step(`^the stand-in receives nothing$`, g.receivesNothing)
	sc.Step(`^the stand-in observes the connection closing$`, g.observesConnectionClosing)
	sc.Step(`^the stand-in disconnects$`, g.standInDisconnects)

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
