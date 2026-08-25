package bootstrap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/infra/config"
)

// This file proves CB5-1-WI-09's deliverable: the seed at
// config/cb5-local-stack.yaml (contracts/stream-data-plane.md,
// cb5-spec's contracts/peripheral-control.md and contracts/dosing-cycle.md)
// declares the three CB5 byte-stream endpoints — cb5/spp, cb5/gps, cb5/gpio —
// and the low-priority defaults CB5::_initGps() and
// CB5::_scanConnectedApplicators() need to clear their boot gate.
//
// It boots a REAL Lyrebird (bootstrap.Run, not a stub) with that exact,
// committed seed file loaded, exactly as cb5local will run it, and drives it
// only through the wire and the Admin REST control plane — never by reaching
// into lyrebird's internals — the same discipline cb5-e2e's own step
// definitions (test/integration/steps/stream_steps_test.go,
// pin_steps_test.go) are held to.
//
// cb5Space is the space the seed declares and cb5-e2e's .env.local
// (LYREBIRD_SPACE) sets.
const cb5Space = "cb5"

// startCB5App boots Lyrebird with the byte-stream plane enabled and the real
// config/cb5-local-stack.yaml seed loaded from its committed location
// (relative to this package, mirroring test/support/main_test.go's own
// "../features" convention for referencing a checked-in fixture tree by
// path rather than duplicating its content into the test).
func startCB5App(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		DataPlaneAddr:    "127.0.0.1:0",
		ControlPlaneAddr: "127.0.0.1:0",
		StreamPlaneAddr:  "127.0.0.1:0",
		DefaultSpace:     cb5Space,
		DBPath:           filepath.Join(dir, "lyrebird.db"),
		SeedDir:          "../../config",
		GCInterval:       time.Hour,
		TrafficTTL:       time.Hour,
		ScriptTimeout:    time.Second,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	app, err := Run(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Cleanup(func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Shutdown(shCtx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return app
}

// cb5Client is one connected stand-in, dialed the same way a firmware fake
// peripheral (src/fake/) will (WI-15/16/17) — a raw TCP client that sends
// the LYREBIRD/1 handshake and nothing else lyrebird-specific.
type cb5Client struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
	reply  string
}

const handshakeTimeout = 3 * time.Second

// connectCB5 dials app's stream plane and occupies endpoint, mirroring
// test/support/steps_stream.go's connect() exactly: same handshake line
// shape, same reply framing.
func connectCB5(t *testing.T, app *App, endpoint string) *cb5Client {
	t.Helper()
	addr := app.StreamAddr()
	if addr == "" {
		t.Fatalf("the byte-stream data plane is not listening")
	}
	dialer := &net.Dialer{Timeout: handshakeTimeout}
	conn, err := dialer.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial stream plane: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	line := fmt.Sprintf("LYREBIRD/1 %s space=%s\r\n", endpoint, cb5Space)
	if _, err := conn.Write([]byte(line)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	reader := bufio.NewReader(conn)
	if err := conn.SetReadDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	reply, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read handshake reply for %q: %v", endpoint, err)
	}
	_ = conn.SetReadDeadline(time.Time{})

	c := &cb5Client{t: t, conn: conn, reader: reader, reply: strings.TrimRight(reply, "\r\n")}
	if c.reply != "OK" {
		t.Fatalf("handshake for %q = %q, want OK", endpoint, c.reply)
	}
	return c
}

// readFrame reads one delimiter-terminated frame, trimming the CRLF/LF
// terminator so callers compare payloads, not wire framing.
func (c *cb5Client) readFrame(timeout time.Duration) (string, error) {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *cb5Client) write(t *testing.T, payload []byte) {
	t.Helper()
	if _, err := c.conn.Write(payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// ── Admin REST control plane ────────────────────────────────────────────
//
// Every control-plane call carries X-Lyrebird-Space explicitly, exactly as
// cb5-e2e's controlRequest does (test/integration/steps/stream_steps_test.go)
// — the suite never relies on LYREBIRD_DEFAULT_SPACE alone, so this test
// doesn't either.

func adminCall(t *testing.T, app *App, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal admin body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, "http://"+app.ControlAddr()+path, reader)
	if err != nil {
		t.Fatalf("build admin request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Lyrebird-Space", cb5Space)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read admin response: %v", err)
	}
	return resp.StatusCode, raw
}

func adminCallOK(t *testing.T, app *App, method, path string, body any) []byte {
	t.Helper()
	status, raw := adminCall(t, app, method, path, body)
	if status < 200 || status > 299 {
		t.Fatalf("%s %s = %d: %s", method, path, status, raw)
	}
	return raw
}

// ── AC 3, claim 1: every CB5 endpoint is occupiable and reports occupied ──

func TestCB5SeedDeclaresAllThreeEndpointsOccupiable(t *testing.T) {
	app := startCB5App(t)

	for _, endpoint := range []string{"cb5/spp", "cb5/gps", "cb5/gpio"} {
		t.Run(endpoint, func(t *testing.T) {
			connectCB5(t, app, endpoint)

			raw := adminCallOK(t, app, http.MethodGet, "/__lyrebird/stream/endpoints", nil)
			var list []dto.EndpointDTO
			if err := json.Unmarshal(raw, &list); err != nil {
				t.Fatalf("unmarshal endpoint list: %v (%s)", err, raw)
			}
			var found *dto.EndpointDTO
			for i := range list {
				if list[i].Name == endpoint {
					found = &list[i]
				}
			}
			if found == nil {
				t.Fatalf("endpoint %q not declared; declared: %+v", endpoint, list)
			}
			if !found.Occupied {
				t.Fatalf("endpoint %q reports occupied=false after a stand-in connected", endpoint)
			}
		})
	}
}

// ── AC 3, claim 2: a received frame is recorded byte-transparently ───────

func TestCB5SppFrameRecordedByteTransparently(t *testing.T) {
	app := startCB5App(t)
	client := connectCB5(t, app, "cb5/spp")

	// A V5 request whose checksum byte is deliberately non-printable
	// (contracts/frame-protocol.md: the checksum is any byte, frequently
	// non-printable), plus the boundary bytes AC 3/Test Plan step 4 calls
	// out by name: 0x00, 0x7F, 0x80, 0xFF.
	frame := []byte{'I', 'N', 'F', '5', 'N', '0', '0', '0', 'N', 'N', '2', 0x00, 0x7F, 0x80, 0xFF, '\r', '\n'}
	client.write(t, frame)

	var entry dto.TrafficSummaryDTO
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw := adminCallOK(t, app, http.MethodGet,
			"/__lyrebird/traffic?path_prefix="+urlEscapeCB5("/cb5/spp"), nil)
		var list []dto.TrafficSummaryDTO
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatalf("unmarshal traffic list: %v (%s)", err, raw)
		}
		found := false
		for _, e := range list {
			if e.Method == "IN" {
				entry = e
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no IN frame recorded on cb5/spp within the deadline")
		}
		time.Sleep(25 * time.Millisecond)
	}

	raw := adminCallOK(t, app, http.MethodGet, "/__lyrebird/traffic/"+entry.ID, nil)
	var detail dto.TrafficDetailDTO
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("unmarshal traffic detail: %v (%s)", err, raw)
	}
	if !bytes.Equal(detail.Request.Body, frame) {
		t.Fatalf("recorded request body = % x, want % x", detail.Request.Body, frame)
	}
}

// ── AC 3, claim 3: emit delivers bytes back with the terminator appended ─

func TestCB5GpioEmitDeliversFrameWithTerminator(t *testing.T) {
	app := startCB5App(t)
	client := connectCB5(t, app, "cb5/gpio")

	// The correct emit_frame shape per internal/adapters/httpadmin/stream.go
	// and internal/adapters/streamplane/build.go: Frame is a JSON STRING
	// carrying either a bare part-list array or {"parts":[...],"raw":bool} —
	// never a top-level "frame" array, never a top-level "raw"/"space" key
	// (that shape is cb5-e2e's own bug, WI-10's to fix, not reproduced here).
	partList, err := json.Marshal([]map[string]any{{"text": "LEVEL 27 1"}})
	if err != nil {
		t.Fatalf("marshal part list: %v", err)
	}
	adminCallOK(t, app, http.MethodPost, "/__lyrebird/stream/emit", map[string]any{
		"endpoint": "cb5/gpio",
		"frame":    string(partList),
	})

	got, err := client.readFrame(handshakeTimeout)
	if err != nil {
		t.Fatalf("read emitted frame: %v", err)
	}
	if got != "LEVEL 27 1" {
		t.Fatalf("emitted frame = %q, want %q", got, "LEVEL 27 1")
	}
}

// ── The six seeded pin-level defaults (Dev Notes' boot-gate table) ───────

func TestCB5SeededPinLevelsAnswerRead(t *testing.T) {
	app := startCB5App(t)
	client := connectCB5(t, app, "cb5/gpio")

	// Every pin the boot gate polls answers "1": presence sensors read
	// directly (1 = connected), position sensors read INVERTED (1 = has not
	// yet arrived) — contracts/dosing-cycle.md, "The three applicators".
	// Seeding 0 on a position sensor would complete every dose instantly.
	cases := []struct {
		pin  int
		role string
	}{
		{14, "presence right"},
		{25, "presence center"},
		{35, "presence left"},
		{27, "position right"},
		{33, "position center"},
		{34, "position left"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("pin_%d_%s", tc.pin, strings.ReplaceAll(tc.role, " ", "_")), func(t *testing.T) {
			client.write(t, []byte(fmt.Sprintf("READ %d\n", tc.pin)))
			got, err := client.readFrame(handshakeTimeout)
			if err != nil {
				t.Fatalf("read LEVEL reply for pin %d: %v", tc.pin, err)
			}
			want := fmt.Sprintf("LEVEL %d 1", tc.pin)
			if got != want {
				t.Fatalf("pin %d (%s): reply = %q, want %q", tc.pin, tc.role, got, want)
			}
		})
	}
}

// ── AC 2/Test Plan step 5: the seeded cb5/gps cadence ─────────────────────

// wantGPRMC is the exact synthetic sentence config/cb5-local-stack.yaml
// seeds on cb5/gps. Coordinates are cb5-e2e's FIXTURE_LATITUDE/
// FIXTURE_LONGITUDE (0000.000/00000.000, mid-Atlantic — LGPD). The checksum
// (5F) is the real GPRMC XOR of every byte between $ and *, recomputed here
// independently of the seed file so a corrupted seed checksum fails this
// test rather than silently asserting whatever the file happens to contain.
const wantGPRMCPayload = "GPRMC,120000.00,A,0000.000,N,00000.000,E,000.0,000.0,010120,,,A"

func gprmcChecksum(payload string) string {
	var cs byte
	for i := 0; i < len(payload); i++ {
		cs ^= payload[i]
	}
	return strings.ToUpper(hex.EncodeToString([]byte{cs}))
}

func TestCB5GpsCadenceStartsRepeatsAndStopsOnDisconnect(t *testing.T) {
	want := "$" + wantGPRMCPayload + "*" + gprmcChecksum(wantGPRMCPayload)
	if want != "$GPRMC,120000.00,A,0000.000,N,00000.000,E,000.0,000.0,010120,,,A*5F" {
		t.Fatalf("test fixture drifted from the seed file's own checksum: got %s", want)
	}

	app := startCB5App(t)
	client := connectCB5(t, app, "cb5/gps")

	first, err := client.readFrame(2 * time.Second)
	if err != nil {
		t.Fatalf("cadence did not start on occupancy: %v", err)
	}
	if first != want {
		t.Fatalf("first cadence frame = %q, want %q", first, want)
	}

	second, err := client.readFrame(2 * time.Second)
	if err != nil {
		t.Fatalf("cadence did not repeat: %v", err)
	}
	if second != want {
		t.Fatalf("second cadence frame = %q, want %q (repeat_last)", second, want)
	}

	// Stops on disconnect: close the stand-in, then prove no further EMIT is
	// recorded across a full interval — the cadence has genuinely stopped,
	// not merely lost its (now-closed) connection to write to.
	_ = client.conn.Close()
	before := countEmits(t, app, "/cb5/gps")
	time.Sleep(1200 * time.Millisecond)
	after := countEmits(t, app, "/cb5/gps")
	if after != before {
		t.Fatalf("cadence kept emitting after disconnect: %d EMIT entries before close, %d after waiting a full interval",
			before, after)
	}
}

func countEmits(t *testing.T, app *App, path string) int {
	t.Helper()
	raw := adminCallOK(t, app, http.MethodGet,
		"/__lyrebird/traffic?path_prefix="+urlEscapeCB5(path)+"&limit=1000", nil)
	var list []dto.TrafficSummaryDTO
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("unmarshal traffic list: %v (%s)", err, raw)
	}
	n := 0
	for _, e := range list {
		if e.Method == "EMIT" {
			n++
		}
	}
	return n
}

// ── AC 2: the seed survives reset; a scenario-authored rule does not ─────

func TestCB5SeedSurvivesReset(t *testing.T) {
	app := startCB5App(t)

	seededNames := []string{
		"cb5-seed-pin-14", "cb5-seed-pin-25", "cb5-seed-pin-35",
		"cb5-seed-pin-27", "cb5-seed-pin-33", "cb5-seed-pin-34",
	}
	scenarioMock := map[string]any{
		"name": "scenario-authored",
		"match": map[string]any{
			"path": "/cb5/gpio",
			"body": []map[string]any{
				{"jsonpath": "$.fields.0", "equals": "READ"},
				{"jsonpath": "$.fields.1", "equals": "999"},
			},
		},
		"projection": map[string]any{"split": " "},
		"action": map[string]any{
			"respond": map[string]any{"body": `[{"text":"LEVEL 999 1"}]`},
		},
	}
	adminCallOK(t, app, http.MethodPost, "/__lyrebird/mocks", scenarioMock)

	adminCallOK(t, app, http.MethodPost, "/__lyrebird/reset", nil)

	raw := adminCallOK(t, app, http.MethodGet, "/__lyrebird/mocks", nil)
	var mocks []dto.MockDTO
	if err := json.Unmarshal(raw, &mocks); err != nil {
		t.Fatalf("unmarshal mock list: %v (%s)", err, raw)
	}
	byName := map[string]bool{}
	for _, m := range mocks {
		byName[m.Name] = true
	}
	for _, name := range seededNames {
		if !byName[name] {
			t.Errorf("seeded mock %q did not survive reset", name)
		}
	}
	if byName["scenario-authored"] {
		t.Errorf("scenario-authored mock survived reset; seeded mocks must outlive it, not the reverse")
	}

	// The gps cadence endpoint itself must also survive: reconnecting still
	// gets a cadence frame rather than "ERR unknown endpoint".
	client := connectCB5(t, app, "cb5/gps")
	if _, err := client.readFrame(2 * time.Second); err != nil {
		t.Fatalf("cb5/gps did not survive reset: %v", err)
	}
}

func urlEscapeCB5(s string) string {
	return strings.NewReplacer("/", "%2F").Replace(s)
}
