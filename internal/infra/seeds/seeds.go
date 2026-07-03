// Package seeds loads the always-on mocks/partitions/upstreams an operator
// mounts at boot (contracts/seed-config.md). Seeded content is held only in
// memory: it is protected from reset/GC/TTL and never written to the
// disposable SQLite store (constitution Principle III, FR-025).
package seeds

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/brienze1/lyrebird/internal/domain"
)

// file mirrors one seed YAML file's shape (contracts/seed-config.md).
type file struct {
	Space     string         `yaml:"space"`
	Upstreams []fileUpstream `yaml:"upstreams"`
	Mocks     []fileMock     `yaml:"mocks"`
}

type fileUpstream struct {
	MatchHost string `yaml:"match_host"`
	TargetURL string `yaml:"target_url"`
}

// fileMock mirrors one seed YAML mock entry (contracts/seed-config.md).
type fileMock struct {
	Name     string      `yaml:"name"`
	Priority int         `yaml:"priority"`
	Match    fileMatch   `yaml:"match"`
	Action   fileActions `yaml:"action"`
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
		return domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{
			Status: a.Respond.Status, Headers: a.Respond.Headers, Body: []byte(a.Respond.Body),
			Template: a.Respond.Template, LatencyMS: a.Respond.LatencyMS,
		}}, nil
	case a.Proxy != nil:
		return domain.Action{Kind: domain.ActionProxy, Proxy: &domain.ProxyAction{
			RewriteRequestScript: a.Proxy.RewriteRequestScript, TransformResponseScript: a.Proxy.TransformResponseScript,
			LatencyMS: a.Proxy.LatencyMS,
		}}, nil
	case a.Fault != nil:
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
}

// Load reads every *.yaml/*.yml file in dir, in lexical order, and merges
// them into one Seeds value. A missing dir is not an error (seeding is
// optional); a duplicate mock name within the same partition across any
// files is a startup error (fail fast, per contracts/seed-config.md).
func Load(dir string) (Seeds, error) {
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
	seenMock := make(map[string]string) // "partition/name" -> source file, for duplicate detection
	seenSpace := make(map[string]bool)  // tracks which partitions we've already recorded

	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return Seeds{}, fmt.Errorf("seeds: read %s: %w", path, err)
		}

		var f file
		if err := yaml.Unmarshal(raw, &f); err != nil {
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
				TargetURL: u.TargetURL,
			})
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

			out.Mocks = append(out.Mocks, domain.Mock{
				// A deterministic id (not a random UUID) so it's stable
				// across restarts — required for FR-009a's tie-break to be
				// stable, and for GET/PUT/DELETE /mocks/{id} to address a
				// seeded mock consistently. Just the (validated slash-free,
				// per-partition-unique) name — NOT "partition/name": Go's
				// ServeMux {id} wildcard matches exactly one path segment,
				// so an id containing "/" would be unroutable.
				ID:        m.Name,
				Partition: space,
				Name:      m.Name,
				Lifetime:  domain.LifetimeSeeded,
				Priority:  m.Priority,
				Match:     m.Match.toDomain(),
				Action:    action,
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
