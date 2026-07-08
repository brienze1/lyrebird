package support

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

// grpcState holds the per-scenario gRPC client results, sharing the booted
// app + control plane through the common appState.
type grpcState struct {
	s        *appState
	lastResp []byte
	lastErr  error
}

// rawClientCodec is the client-side twin of grpcplane's server codec: it
// moves raw message bytes verbatim so the test can send/receive hand-built
// protobuf without any generated stubs. Name "proto" so the content-type is
// the application/grpc+proto a real client would send.
type rawClientCodec struct{}

func (rawClientCodec) Marshal(v any) ([]byte, error) { return *(v.(*[]byte)), nil }
func (rawClientCodec) Unmarshal(data []byte, v any) error {
	*(v.(*[]byte)) = append([]byte(nil), data...)
	return nil
}
func (rawClientCodec) Name() string { return "proto" }

func (g *grpcState) enableGRPC() error {
	g.s.grpcEnabled = true
	return nil
}

// postJSON POSTs a JSON body with a context (satisfying the noctx linter, and
// matching the rest of the suite's HTTP style).
func (g *grpcState) postJSON(url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// createGRPCMock creates an ephemeral gRPC mock over the control plane (REST),
// exactly as any agent would — the respond body is the response field-spec.
func (g *grpcState) createGRPCMock(method string, body *godog.DocString) error {
	payload := map[string]any{
		"name": "grpc-" + sanitizeName(method),
		"match": map[string]any{
			"method": "POST",
			"path":   method,
		},
		"action": map[string]any{
			"respond": map[string]any{"body": body.Content},
		},
	}
	raw, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://%s/__lyrebird/mocks", g.s.app.ControlAddr())
	resp, err := g.postJSON(url, raw)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/mocks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("create gRPC mock status = %d, want 200/201", resp.StatusCode)
	}
	return nil
}

// createGRPCMockMatchingField creates a gRPC mock that additionally matches on
// a request message field, driving projectForMatch -> the existing gjson body
// matcher end-to-end. Length-delimited fields are projected as base64 under
// "fN" and as the decoded string under "fN_str", so a string-field matcher
// targets "fN_str".
func (g *grpcState) createGRPCMockMatchingField(method string, fieldNum int, wantValue string, body *godog.DocString) error {
	payload := map[string]any{
		"name": "grpc-match-" + sanitizeName(method),
		"match": map[string]any{
			"method": "POST",
			"path":   method,
			"body": []map[string]any{
				{"jsonpath": fmt.Sprintf("f%d_str", fieldNum), "equals": wantValue},
			},
		},
		"action": map[string]any{
			"respond": map[string]any{"body": body.Content},
		},
	}
	raw, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://%s/__lyrebird/mocks", g.s.app.ControlAddr())
	resp, err := g.postJSON(url, raw)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/mocks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("create field-matching gRPC mock status = %d, want 200/201", resp.StatusCode)
	}
	return nil
}

// loadRecipeAsMock fetches a shipped recipe over the control plane and posts
// its mock payload to /__lyrebird/mocks — proving the actual embedded recipe
// (not a hand-copied body) works end-to-end over gRPC.
func (g *grpcState) loadRecipeAsMock(id string) error {
	getURL := fmt.Sprintf("http://%s/__lyrebird/examples/%s", g.s.app.ControlAddr(), id)
	getReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, getURL, nil)
	if err != nil {
		return fmt.Errorf("build GET example %s request: %w", id, err)
	}
	resp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		return fmt.Errorf("GET example %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET example %s status = %d", id, resp.StatusCode)
	}
	var recipe struct {
		Mock json.RawMessage `json:"mock"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&recipe); err != nil {
		return fmt.Errorf("decode recipe %s: %w", id, err)
	}
	if len(recipe.Mock) == 0 {
		return fmt.Errorf("recipe %s has no mock payload", id)
	}
	postURL := fmt.Sprintf("http://%s/__lyrebird/mocks", g.s.app.ControlAddr())
	pr, err := g.postJSON(postURL, recipe.Mock)
	if err != nil {
		return fmt.Errorf("POST recipe %s mock: %w", id, err)
	}
	defer func() { _ = pr.Body.Close() }()
	if pr.StatusCode != http.StatusCreated && pr.StatusCode != http.StatusOK {
		return fmt.Errorf("POST recipe %s mock status = %d", id, pr.StatusCode)
	}
	return nil
}

// seededGRPCMock writes a seed YAML for a gRPC mock into the seed dir; it must
// run BEFORE a (re)boot so the mock loads as a protected seeded mock.
func (g *grpcState) seededGRPCMock(name, method string, body *godog.DocString) error {
	yaml := fmt.Sprintf("mocks:\n"+
		"  - name: %s\n"+
		"    match:\n"+
		"      method: POST\n"+
		"      path: %s\n"+
		"    action:\n"+
		"      respond:\n"+
		"        body: '%s'\n", name, method, body.Content)
	return os.WriteFile(filepath.Join(g.s.seedDir, name+".yaml"), []byte(yaml), 0o644)
}

func (g *grpcState) callGRPCStringField(method string, fieldNum int, value string) error {
	var req []byte
	req = protowire.AppendTag(req, protowire.Number(fieldNum), protowire.BytesType)
	req = protowire.AppendString(req, value)
	return g.invoke(method, req)
}

func (g *grpcState) invoke(method string, req []byte) error {
	addr := g.s.app.GRPCAddr()
	if addr == "" {
		return fmt.Errorf("gRPC data plane is not enabled for this scenario")
	}
	conn, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	var resp []byte
	err = conn.Invoke(context.Background(), method, &req, &resp, grpc.ForceCodec(rawClientCodec{}))
	g.lastResp, g.lastErr = resp, err
	return nil // outcome asserted by a later step
}

func (g *grpcState) callSucceeds() error {
	if g.lastErr != nil {
		return fmt.Errorf("expected gRPC call to succeed, got: %w", g.lastErr)
	}
	return nil
}

func (g *grpcState) callFailsWithStatus(want string) error {
	if g.lastErr == nil {
		return fmt.Errorf("expected gRPC call to fail with %s, but it succeeded", want)
	}
	got := status.Code(g.lastErr).String()
	if got != want {
		return fmt.Errorf("gRPC status = %s, want %s (err: %w)", got, want, g.lastErr)
	}
	return nil
}

func (g *grpcState) responseFieldEqualsString(fieldNum int, want string) error {
	f, ok := g.field(protowire.Number(fieldNum))
	if !ok {
		return fmt.Errorf("response has no field %d", fieldNum)
	}
	if got := string(f.bytes); got != want {
		return fmt.Errorf("response field %d = %q, want %q", fieldNum, got, want)
	}
	return nil
}

func (g *grpcState) responseFieldEqualsInt(fieldNum int, want int64) error {
	f, ok := g.field(protowire.Number(fieldNum))
	if !ok {
		return fmt.Errorf("response has no field %d", fieldNum)
	}
	if int64(f.varint) != want {
		return fmt.Errorf("response field %d = %d, want %d", fieldNum, f.varint, want)
	}
	return nil
}

// clientField is one decoded response field (test-side twin of rawField).
type clientField struct {
	typ    protowire.Type
	varint uint64
	bytes  []byte
}

func (g *grpcState) field(num protowire.Number) (clientField, bool) {
	b := g.lastResp
	for len(b) > 0 {
		n, typ, tagLen := protowire.ConsumeTag(b)
		if tagLen < 0 {
			return clientField{}, false
		}
		b = b[tagLen:]
		var valLen int
		var cf clientField
		cf.typ = typ
		switch typ {
		case protowire.VarintType:
			cf.varint, valLen = protowire.ConsumeVarint(b)
		case protowire.Fixed32Type:
			_, valLen = protowire.ConsumeFixed32(b)
		case protowire.Fixed64Type:
			_, valLen = protowire.ConsumeFixed64(b)
		case protowire.BytesType:
			var v []byte
			v, valLen = protowire.ConsumeBytes(b)
			cf.bytes = append([]byte(nil), v...)
		default:
			return clientField{}, false
		}
		if valLen < 0 {
			return clientField{}, false
		}
		b = b[valLen:]
		if n == num {
			return cf, true
		}
	}
	return clientField{}, false
}

func sanitizeName(method string) string {
	out := make([]rune, 0, len(method))
	for _, r := range method {
		if r == '/' || r == '.' {
			out = append(out, '-')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// RegisterGRPCSteps wires the generic gRPC data-plane steps.
func RegisterGRPCSteps(sc *godog.ScenarioContext, s *appState) {
	g := &grpcState{s: s}
	sc.Step(`^the gRPC data plane is enabled$`, g.enableGRPC)
	sc.Step(`^the recipe "([^"]*)" is loaded as a mock$`, g.loadRecipeAsMock)
	sc.Step(`^a gRPC mock for method "([^"]*)" responding with:$`, g.createGRPCMock)
	sc.Step(`^a gRPC mock for method "([^"]*)" matching field (\d+) string "([^"]*)" responding with:$`, g.createGRPCMockMatchingField)
	sc.Step(`^a seeded gRPC mock "([^"]*)" for method "([^"]*)" responding with:$`, g.seededGRPCMock)
	sc.Step(`^I call gRPC method "([^"]*)" with field (\d+) set to string "([^"]*)"$`, g.callGRPCStringField)
	sc.Step(`^the gRPC call succeeds$`, g.callSucceeds)
	sc.Step(`^the gRPC call fails with status "([^"]*)"$`, g.callFailsWithStatus)
	sc.Step(`^the gRPC response field (\d+) equals string "([^"]*)"$`, g.responseFieldEqualsString)
	sc.Step(`^the gRPC response field (\d+) equals int (\d+)$`, g.responseFieldEqualsInt)
}
