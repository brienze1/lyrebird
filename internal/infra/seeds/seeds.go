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

// fileMock is intentionally minimal at M0: only the fields needed to prove a
// seeded mock exists and is addressable by name/partition. Full match/action
// parsing is added when the matcher/respond engine lands (M2).
type fileMock struct {
	Name     string `yaml:"name"`
	Priority int    `yaml:"priority"`
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
			key := space + "/" + m.Name
			if prior, dup := seenMock[key]; dup {
				return Seeds{}, fmt.Errorf(
					"seeds: %w: mock %q in partition %q declared in both %s and %s",
					domain.ErrDuplicateID, m.Name, space, prior, path,
				)
			}
			seenMock[key] = path

			out.Mocks = append(out.Mocks, domain.Mock{
				Partition: space,
				Name:      m.Name,
				Lifetime:  domain.LifetimeSeeded,
				Priority:  m.Priority,
			})
		}
	}

	return out, nil
}
