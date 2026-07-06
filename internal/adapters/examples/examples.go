// Package examples hosts Lyrebird's embedded, ready-to-adapt cloud/SDK
// mocking recipe library shared by the mcp and httpadmin adapters.
package examples

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed recipes/*.json
var recipeFS embed.FS

// Recipe is one recipe library entry; Mock is a ready-to-adapt create_mock
// payload, nil for guidance-only entries with no single API to match.
type Recipe struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Provider    string          `json:"provider"`
	Service     string          `json:"service"`
	Description string          `json:"description"`
	Mock        json.RawMessage `json:"mock,omitempty"`
}

// Summary is a Recipe with Mock omitted — what List returns, so a caller
// doesn't have to fetch every full recipe just to browse the index.
type Summary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Provider    string `json:"provider"`
	Service     string `json:"service"`
	Description string `json:"description"`
}

var recipes = mustLoadRecipes()

// mustLoadRecipes parses every embedded recipes/*.json file at package init.
// Panics on a malformed file: a broken embed is a build-time bug, not a runtime case callers should handle.
func mustLoadRecipes() []Recipe {
	entries, err := recipeFS.ReadDir("recipes")
	if err != nil {
		panic(fmt.Sprintf("examples: read embedded recipes directory: %v", err))
	}
	out := make([]Recipe, 0, len(entries))
	for _, entry := range entries {
		raw, err := recipeFS.ReadFile("recipes/" + entry.Name())
		if err != nil {
			panic(fmt.Sprintf("examples: read embedded recipe %q: %v", entry.Name(), err))
		}
		var r Recipe
		if err := json.Unmarshal(raw, &r); err != nil {
			panic(fmt.Sprintf("examples: parse embedded recipe %q: %v", entry.Name(), err))
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// List returns every recipe as a Summary, filtered to those whose
// id/title/provider/service contains query (case-insensitive); empty query returns all.
func List(query string) []Summary {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]Summary, 0, len(recipes))
	for _, r := range recipes {
		if q != "" && !recipeMatches(r, q) {
			continue
		}
		out = append(out, Summary{ID: r.ID, Title: r.Title, Provider: r.Provider, Service: r.Service, Description: r.Description})
	}
	return out
}

// recipeMatches searches only structured metadata (id/title/provider/
// service) — not the free-text description, to avoid other providers' names in prose causing false matches.
func recipeMatches(r Recipe, q string) bool {
	haystacks := []string{r.ID, r.Title, r.Provider, r.Service}
	for _, h := range haystacks {
		if strings.Contains(strings.ToLower(h), q) {
			return true
		}
	}
	return false
}

// Get returns the full recipe (including its Mock payload, if any) for id.
func Get(id string) (Recipe, bool) {
	for _, r := range recipes {
		if r.ID == id {
			return r, true
		}
	}
	return Recipe{}, false
}
