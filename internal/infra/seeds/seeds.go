// Package seeds loads the always-on mocks/partitions/upstreams an operator
// mounts at boot (contracts/seed-config.md). Seeded content is held only in
// memory: it is protected from reset/GC/TTL and never written to the
// disposable SQLite store (constitution Principle III, FR-025).
package seeds

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/brienze1/lyrebird/internal/domain"
)

// scriptValidator is the subset of usecase.ScriptEval Load needs: parse
// validation only, at boot, before any request is ever served.
type scriptValidator interface {
	ValidateScript(src string) error
}

// file mirrors one seed YAML file's shape (contracts/seed-config.md).
type file struct {
	Space     string         `yaml:"space"`
	Upstreams []fileUpstream `yaml:"upstreams"`
	Endpoints []fileEndpoint `yaml:"endpoints"`
	Mocks     []fileMock     `yaml:"mocks"`
}

// fileEndpoint mirrors one seed YAML byte-stream endpoint entry
// (003's contracts/stream-data-plane.md).
type fileEndpoint struct {
	Name       string          `yaml:"name"`
	Framing    fileFraming     `yaml:"framing"`
	Projection *fileProjection `yaml:"projection"`
	Cadence    *fileCadence    `yaml:"cadence"`
}

// fileFraming mirrors an endpoint's framing block. The variant is selected by
// which field is present — the same presence-not-a-kind-key convention
// fileActions already uses for respond/proxy/fault — so an author writes
// `{delimiter: "\r\n"}` and means it.
type fileFraming struct {
	Delimiter string      `yaml:"delimiter"`
	Length    int         `yaml:"length"`
	Prefix    *filePrefix `yaml:"prefix"`
}

type filePrefix struct {
	Width  int    `yaml:"width"`
	Endian string `yaml:"endian"`
}

// fileProjection mirrors a projection block, which appears both on an
// endpoint (as its default) and on a mock (as that rule's override) — one
// shape, two homes, per 003's FR-006.
type fileProjection struct {
	Split string                `yaml:"split"`
	At    []fileProjectionField `yaml:"at"`
}

type fileProjectionField struct {
	Name   string `yaml:"name"`
	Offset int    `yaml:"offset"`
	Length int    `yaml:"length"`
	As     string `yaml:"as"`
}

// fileCadence mirrors an endpoint's cadence block. Interval is a string
// rather than a time.Duration because yaml.v3 decodes a bare number into a
// Duration as NANOSECONDS — so `interval: 100` would silently mean 100ns, not
// 100ms — and parsing it explicitly makes `100ms` mean what an author reads
// it as.
type fileCadence struct {
	Interval     string       `yaml:"interval"`
	OnExhaustion string       `yaml:"on_exhaustion"`
	Frames       [][]filePart `yaml:"frames"`
}

// filePart mirrors one part of the frame-spec grammar (003's data-model.md
// §5) in YAML. It is a seeds-layer decoder for the same grammar the stream
// plane decodes from JSON, exactly as fileMatch and fileActions are
// seeds-layer decoders for domain.Match and domain.Action — each edge decodes
// its own wire format into the shared domain type.
type filePart struct {
	Text     *string   `yaml:"text"`
	Hex      *string   `yaml:"hex"`
	Int      *int64    `yaml:"int"`
	CopyFrom *string   `yaml:"copyFrom"`
	Width    int       `yaml:"width"`
	Pad      string    `yaml:"pad"`
	Repeat   *filePart `yaml:"repeat"`
	Times    int       `yaml:"times"`
}

func (p filePart) toDomain() domain.FramePart {
	out := domain.FramePart{
		Text: p.Text, Hex: p.Hex, Int: p.Int, CopyFrom: p.CopyFrom,
		Width: p.Width, Pad: p.Pad, Times: p.Times,
	}
	if p.Repeat != nil {
		inner := p.Repeat.toDomain()
		out.Repeat = &inner
	}
	return out
}

func (p fileProjection) toDomain() *domain.Projection {
	out := &domain.Projection{Split: p.Split}
	for _, f := range p.At {
		out.At = append(out.At, domain.ProjectionField{
			Name: f.Name, Offset: f.Offset, Length: f.Length, As: domain.ProjectionAs(f.As),
		})
	}
	return out
}

// toDomain resolves the framing variant from which field the author set,
// rejecting an ambiguous block outright: two variants set at once has no
// single correct reading, and guessing one would make the same bytes mean
// different things on different boots.
func (f fileFraming) toDomain(sourcePath, endpoint string) (domain.Framing, error) {
	set := 0
	if f.Delimiter != "" {
		set++
	}
	if f.Length > 0 {
		set++
	}
	if f.Prefix != nil {
		set++
	}
	switch {
	case set == 0:
		return domain.Framing{}, fmt.Errorf(
			"seeds: %s: endpoint %q: framing must set exactly one of delimiter/length/prefix", sourcePath, endpoint)
	case set > 1:
		return domain.Framing{}, fmt.Errorf(
			"seeds: %s: endpoint %q: framing sets more than one of delimiter/length/prefix", sourcePath, endpoint)
	case f.Delimiter != "":
		return domain.Framing{Kind: domain.FramingDelimiter, Delimiter: []byte(f.Delimiter)}, nil
	case f.Length > 0:
		return domain.Framing{Kind: domain.FramingLength, Length: f.Length}, nil
	default:
		endian := domain.Endianness(f.Prefix.Endian)
		if endian == "" {
			endian = domain.EndianBig
		}
		return domain.Framing{Kind: domain.FramingPrefix, PrefixWidth: f.Prefix.Width, PrefixEndian: endian}, nil
	}
}

func (c fileCadence) toDomain(sourcePath, endpoint string) (*domain.Cadence, error) {
	interval, err := time.ParseDuration(c.Interval)
	if err != nil {
		return nil, fmt.Errorf("seeds: %s: endpoint %q: cadence.interval %q is not a duration (e.g. 100ms): %w",
			sourcePath, endpoint, c.Interval, err)
	}
	out := &domain.Cadence{Interval: interval, OnExhaust: domain.OnExhaustion(c.OnExhaustion)}
	if out.OnExhaust == "" {
		out.OnExhaust = domain.OnExhaustionRepeatLast
	}
	for _, frame := range c.Frames {
		parts := make([]domain.FramePart, 0, len(frame))
		for _, p := range frame {
			parts = append(parts, p.toDomain())
		}
		out.Frames = append(out.Frames, parts)
	}
	return out, nil
}

type fileUpstream struct {
	MatchHost string `yaml:"match_host"`
	MatchPath string `yaml:"match_path"`
	TargetURL string `yaml:"target_url"`
}

// fileMock mirrors one seed YAML mock entry (contracts/seed-config.md).
type fileMock struct {
	Name       string          `yaml:"name"`
	Priority   int             `yaml:"priority"`
	Group      string          `yaml:"group"`
	Match      fileMatch       `yaml:"match"`
	Script     *fileScript     `yaml:"script"`
	Action     fileActions     `yaml:"action"`
	Scenario   *fileScenario   `yaml:"scenario"`
	Projection *fileProjection `yaml:"projection"`
}

// fileScript mirrors one seed YAML mock's script block (domain.Script).
type fileScript struct {
	MatchSrc   string `yaml:"match_src"`
	RespondSrc string `yaml:"respond_src"`
}

// fileScenario mirrors one seed YAML mock's scenario block (domain.Scenario),
// reusing fileRespond for each response instead of duplicating its shape.
type fileScenario struct {
	Responses []fileRespond `yaml:"responses"`
	OnExhaust string        `yaml:"on_exhaust"`
}

// fileMatch mirrors the seed YAML's match block. Matcher fields are
// flattened directly onto each header/query entry (no separate "matcher"
// wrapper key) and onto each body entry alongside its jsonpath, exactly as
// contracts/seed-config.md's example shows — the same shape Admin REST's
// mock JSON DTOs use, since import/export round-trips through this schema.
type fileMatch struct {
	Method  string                 `yaml:"method"`
	Path    string                 `yaml:"path"`
	Headers map[string]fileMatcher `yaml:"headers"`
	Query   map[string]fileMatcher `yaml:"query"`
	Body    []fileBodyMatcher      `yaml:"body"`
}

type fileMatcher struct {
	Equals   *string `yaml:"equals"`
	Contains *string `yaml:"contains"`
	Regex    *string `yaml:"regex"`
	Exists   *bool   `yaml:"exists"`
}

type fileBodyMatcher struct {
	JSONPath    string `yaml:"jsonpath"`
	fileMatcher `yaml:",inline"`
}

// fileActions mirrors the seed YAML's action block: exactly one of
// respond/proxy/fault is present, and that presence — not a separate "kind"
// key — is what selects the ActionKind.
type fileActions struct {
	Respond *fileRespond `yaml:"respond"`
	Proxy   *fileProxy   `yaml:"proxy"`
	Fault   *fileFault   `yaml:"fault"`
}

type fileRespond struct {
	Status    int               `yaml:"status"`
	Headers   map[string]string `yaml:"headers"`
	Body      string            `yaml:"body"`
	Template  bool              `yaml:"template"`
	LatencyMS *int              `yaml:"latency_ms"`
}

type fileProxy struct {
	RewriteRequestScript    *string `yaml:"rewrite_request"`
	TransformResponseScript *string `yaml:"transform_response"`
	LatencyMS               *int    `yaml:"latency_ms"`
}

type fileFault struct {
	Kind    string `yaml:"kind"`
	DelayMS *int   `yaml:"delay_ms"`
}

func (r fileRespond) toDomain() domain.RespondAction {
	return domain.RespondAction{
		Status: r.Status, Headers: r.Headers, Body: []byte(r.Body),
		Template: r.Template, LatencyMS: r.LatencyMS,
	}
}

func (s fileScript) toDomain() *domain.Script {
	return &domain.Script{MatchSrc: s.MatchSrc, RespondSrc: s.RespondSrc}
}

func (sc fileScenario) toDomain() *domain.Scenario {
	out := &domain.Scenario{OnExhaust: domain.OnExhaust(sc.OnExhaust)}
	for _, r := range sc.Responses {
		out.Responses = append(out.Responses, r.toDomain())
	}
	return out
}

func (fm fileMatcher) toDomain() domain.Matcher {
	return domain.Matcher{Equals: fm.Equals, Contains: fm.Contains, Regex: fm.Regex, Exists: fm.Exists}
}

func (m fileMatch) toDomain() domain.Match {
	out := domain.Match{Method: m.Method, Path: m.Path}
	if len(m.Headers) > 0 {
		out.Headers = make(map[string]domain.Matcher, len(m.Headers))
		for k, v := range m.Headers {
			out.Headers[k] = v.toDomain()
		}
	}
	if len(m.Query) > 0 {
		out.Query = make(map[string]domain.Matcher, len(m.Query))
		for k, v := range m.Query {
			out.Query[k] = v.toDomain()
		}
	}
	for _, b := range m.Body {
		out.Body = append(out.Body, domain.BodyMatcher{Path: b.JSONPath, Matcher: b.toDomain()})
	}
	return out
}

func (a fileActions) toDomain(sourcePath string) (domain.Action, error) {
	switch {
	case a.Respond != nil:
		respond := a.Respond.toDomain()
		return domain.Action{Kind: domain.ActionRespond, Respond: &respond}, nil
	case a.Proxy != nil:
		return domain.Action{Kind: domain.ActionProxy, Proxy: &domain.ProxyAction{
			RewriteRequestScript: a.Proxy.RewriteRequestScript, TransformResponseScript: a.Proxy.TransformResponseScript,
			LatencyMS: a.Proxy.LatencyMS,
		}}, nil
	case a.Fault != nil:
		switch domain.FaultKind(a.Fault.Kind) {
		case domain.FaultDelay, domain.FaultReset, domain.FaultTimeout, domain.FaultMalformed:
		default:
			return domain.Action{}, fmt.Errorf("seeds: %s: fault.kind %q must be one of delay/reset/timeout/malformed", sourcePath, a.Fault.Kind)
		}
		return domain.Action{Kind: domain.ActionFault, Fault: &domain.FaultAction{
			Kind: domain.FaultKind(a.Fault.Kind), DelayMS: a.Fault.DelayMS,
		}}, nil
	default:
		return domain.Action{}, fmt.Errorf("seeds: %s: mock action must set exactly one of respond/proxy/fault", sourcePath)
	}
}

// Seeds is the fully-loaded, in-memory result of reading every file in a
// seed directory.
type Seeds struct {
	Partitions []domain.Partition
	Mocks      []domain.Mock
	Upstreams  []domain.Upstream
	Endpoints  []domain.Endpoint
}

// Load reads every *.yaml/*.yml file in dir, in lexical order, and merges
// them into one Seeds value. A missing dir is not an error (seeding is
// optional); a duplicate mock name within the same partition across any
// files is a startup error (fail fast, per contracts/seed-config.md). script
// validates any script block's match_src/respond_src at boot (also per
// contracts/seed-config.md), before any request is ever served.
func Load(dir string, script scriptValidator) (Seeds, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return Seeds{}, nil
	}
	if err != nil {
		return Seeds{}, fmt.Errorf("seeds: read dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext == ".yaml" || ext == ".yml" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var out Seeds
	seenMock := make(map[string]string)     // "partition/name" -> source file, for duplicate detection
	seenEndpoint := make(map[string]string) // "partition/name" -> source file, same duplicate rule
	seenSpace := make(map[string]bool)      // tracks which partitions we've already recorded

	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return Seeds{}, fmt.Errorf("seeds: read %s: %w", path, err)
		}

		var f file
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
			return Seeds{}, fmt.Errorf("seeds: parse %s: %w", path, err)
		}

		space := f.Space
		if space == "" {
			space = domain.DefaultPartitionID
		}
		if !seenSpace[space] {
			seenSpace[space] = true
			out.Partitions = append(out.Partitions, domain.Partition{ID: space})
		}

		for _, u := range f.Upstreams {
			out.Upstreams = append(out.Upstreams, domain.Upstream{
				Partition: space,
				MatchHost: u.MatchHost,
				MatchPath: u.MatchPath,
				TargetURL: u.TargetURL,
			})
		}

		for _, e := range f.Endpoints {
			// A handshake resolves one name to exactly one endpoint, so two
			// declarations of the same name would make which one a stand-in
			// got depend on file order — the same fail-fast rule mock names
			// already carry.
			key := space + "/" + e.Name
			if prior, dup := seenEndpoint[key]; dup {
				return Seeds{}, fmt.Errorf(
					"seeds: %w: endpoint %q in space %q declared in both %s and %s",
					domain.ErrDuplicateID, e.Name, space, prior, path,
				)
			}
			seenEndpoint[key] = path

			endpoint, err := e.toDomain(path, space)
			if err != nil {
				return Seeds{}, err
			}
			out.Endpoints = append(out.Endpoints, endpoint)
		}

		for _, m := range f.Mocks {
			if strings.Contains(m.Name, "/") {
				return Seeds{}, fmt.Errorf("seeds: %s: mock name %q must not contain \"/\" (used as its id in GET/PUT/DELETE /mocks/{id}, a single path segment)", path, m.Name)
			}
			key := space + "/" + m.Name
			if prior, dup := seenMock[key]; dup {
				return Seeds{}, fmt.Errorf(
					"seeds: %w: mock %q in partition %q declared in both %s and %s",
					domain.ErrDuplicateID, m.Name, space, prior, path,
				)
			}
			seenMock[key] = path

			action, err := m.Action.toDomain(path)
			if err != nil {
				return Seeds{}, err
			}

			var domainScript *domain.Script
			if m.Script != nil {
				if err := script.ValidateScript(m.Script.MatchSrc); err != nil {
					return Seeds{}, fmt.Errorf("seeds: %s: script.match_src: %w", path, err)
				}
				if err := script.ValidateScript(m.Script.RespondSrc); err != nil {
					return Seeds{}, fmt.Errorf("seeds: %s: script.respond_src: %w", path, err)
				}
				domainScript = m.Script.toDomain()
			}

			var domainScenario *domain.Scenario
			if m.Scenario != nil {
				domainScenario = m.Scenario.toDomain()
			}

			out.Mocks = append(out.Mocks, domain.Mock{
				// A deterministic id (not a random UUID) so it's stable
				// across restarts — required for FR-009a's tie-break to be
				// stable, and for GET/PUT/DELETE /mocks/{id} to address a
				// seeded mock consistently. Just the (validated slash-free,
				// per-partition-unique) name — NOT "partition/name": Go's
				// ServeMux {id} wildcard matches exactly one path segment,
				// so an id containing "/" would be unroutable.
				//
				// This name-shaped id space and idgen.UUID()'s random v4
				// UUID space (used for ephemeral mocks, see mock_crud.go) are
				// never enforced disjoint by any type system or DB
				// constraint — seeded mocks are never persisted to the
				// ephemeral_mocks table at all, so its PRIMARY KEY offers no
				// protection. In practice they're disjoint by construction:
				// ephemeral ids are always server-generated (MockInput has
				// no caller-supplied id field), so no caller can force a
				// collision, and an operator would have to deliberately name
				// a seed after a real UUID for one to occur. Audited in
				// refactor pass 10; not a bug, just previously unstated.
				ID:         m.Name,
				Partition:  space,
				Name:       m.Name,
				Lifetime:   domain.LifetimeSeeded,
				Priority:   m.Priority,
				Group:      m.Group,
				Match:      m.Match.toDomain(),
				Script:     domainScript,
				Action:     action,
				Scenario:   domainScenario,
				Projection: projectionOrNil(m.Projection),
				// Synthetic zero time: any ephemeral mock of equal priority
				// always outranks a seeded one in FR-009a's tie-break (newer
				// wins) — API overrides beat static config by design.
				CreatedAt: time.Unix(0, 0).UTC(),
			})
		}
	}

	return out, nil
}

// SeededMocks implements usecase.SeededMockSource: every seeded mock loaded
// into s that belongs to partition.
func (s Seeds) SeededMocks(partition string) []domain.Mock {
	var out []domain.Mock
	for _, m := range s.Mocks {
		if m.Partition == partition {
			out = append(out, m)
		}
	}
	return out
}

// SeededEndpoints implements usecase.SeededEndpointSource: every seeded
// byte-stream endpoint loaded into s that belongs to partition.
func (s Seeds) SeededEndpoints(partition string) []domain.Endpoint {
	var out []domain.Endpoint
	for _, e := range s.Endpoints {
		if e.Partition == partition {
			out = append(out, e)
		}
	}
	return out
}

// projectionOrNil keeps a mock with no projection block at nil rather than at
// an empty Projection, so "inherit the endpoint's default" and "override it
// with nothing" stay distinguishable (003's FR-006).
func projectionOrNil(p *fileProjection) *domain.Projection {
	if p == nil {
		return nil
	}
	return p.toDomain()
}

// SeededUpstreams implements usecase.SeededUpstreamSource: every seeded
// upstream loaded into s that belongs to partition.
func (s Seeds) SeededUpstreams(partition string) []domain.Upstream {
	var out []domain.Upstream
	for _, u := range s.Upstreams {
		if u.Partition == partition {
			out = append(out, u)
		}
	}
	return out
}

// toDomain converts one seed endpoint entry, resolving its framing variant
// and defaulting its cadence's exhaustion behaviour. Seeded endpoints carry
// LifetimeSeeded, so reset and GC leave them alone exactly as they leave
// seeded mocks alone (003's FR-022).
func (e fileEndpoint) toDomain(sourcePath, space string) (domain.Endpoint, error) {
	if e.Name == "" {
		return domain.Endpoint{}, fmt.Errorf("seeds: %s: endpoint name is required", sourcePath)
	}
	// A name MAY contain "/" (a namespaced family like "cb5/spp") — mirrors
	// usecase.validateEndpoint's rule exactly, so a name accepted here is
	// never later rejected by create_endpoint at runtime, or the reverse.
	// DELETE /__lyrebird/stream/endpoints/{name...} uses Go 1.22 ServeMux's
	// trailing wildcard, which captures the whole remainder undivided; what
	// is still rejected is punctuation that would make that path AMBIGUOUS
	// to reconstruct, and a "." or ".." segment, which http.ServeMux cleans
	// out of the request path before pattern matching — an endpoint seeded
	// with one would be permanently undeletable through that route.
	if strings.HasPrefix(e.Name, "/") || strings.HasSuffix(e.Name, "/") {
		return domain.Endpoint{}, fmt.Errorf(
			"seeds: %s: endpoint name %q must not start or end with \"/\"", sourcePath, e.Name)
	}
	if strings.Contains(e.Name, "//") {
		return domain.Endpoint{}, fmt.Errorf(
			"seeds: %s: endpoint name %q must not contain an empty path segment (\"//\")", sourcePath, e.Name)
	}
	if clean := path.Clean("/" + e.Name); clean != "/"+e.Name {
		return domain.Endpoint{}, fmt.Errorf(
			"seeds: %s: endpoint name %q must not contain a \".\" or \"..\" path segment", sourcePath, e.Name)
	}
	if strings.ContainsAny(e.Name, " \t\r\n") {
		return domain.Endpoint{}, fmt.Errorf(
			"seeds: %s: endpoint name %q must not contain whitespace", sourcePath, e.Name)
	}

	framing, err := e.Framing.toDomain(sourcePath, e.Name)
	if err != nil {
		return domain.Endpoint{}, err
	}

	var cadence *domain.Cadence
	if e.Cadence != nil {
		if cadence, err = e.Cadence.toDomain(sourcePath, e.Name); err != nil {
			return domain.Endpoint{}, err
		}
		if len(cadence.Frames) == 0 {
			return domain.Endpoint{}, fmt.Errorf(
				"seeds: %s: endpoint %q: cadence.frames must not be empty", sourcePath, e.Name)
		}
	}

	return domain.Endpoint{
		Name:       e.Name,
		Partition:  space,
		Framing:    framing,
		Projection: projectionOrNil(e.Projection),
		Cadence:    cadence,
		Lifetime:   domain.LifetimeSeeded,
		// Synthetic zero time, matching seeded mocks: nothing tie-breaks on
		// an endpoint's creation time, but a stable value keeps an export
		// byte-identical across restarts.
		CreatedAt: time.Unix(0, 0).UTC(),
	}, nil
}
