package examples

import (
	"encoding/json"
	"testing"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
)

func TestList_ReturnsEveryRecipeWithNoQuery(t *testing.T) {
	got := List("")
	if len(got) != 9 {
		t.Fatalf("List(\"\") returned %d entries, want 9", len(got))
	}
}

func TestList_FiltersByProviderSubstring(t *testing.T) {
	got := List("aws")
	if len(got) != 5 {
		t.Fatalf("List(\"aws\") returned %d entries, want 5", len(got))
	}
	for _, s := range got {
		if s.Provider != "aws" {
			t.Errorf("List(\"aws\") returned non-aws entry %+v", s)
		}
	}
}

func TestList_IsCaseInsensitive(t *testing.T) {
	if got := List("AWS"); len(got) != 5 {
		t.Errorf("List(\"AWS\") returned %d entries, want 5", len(got))
	}
}

func TestList_SummariesHaveIDAndTitle(t *testing.T) {
	for _, s := range List("") {
		if s.ID == "" || s.Title == "" {
			t.Errorf("summary %+v missing id/title", s)
		}
	}
}

func TestGet_ReturnsTheFullRecipe(t *testing.T) {
	r, ok := Get("aws-sns")
	if !ok {
		t.Fatal("Get(\"aws-sns\") not found")
	}
	if r.Mock == nil {
		t.Fatal("aws-sns recipe has no mock payload")
	}
}

func TestGet_TheHowToEntryHasNoMockPayload(t *testing.T) {
	r, ok := Get("endpoint-injection-howto")
	if !ok {
		t.Fatal("Get(\"endpoint-injection-howto\") not found")
	}
	if r.Mock != nil {
		t.Errorf("endpoint-injection-howto unexpectedly has a mock payload: %s", r.Mock)
	}
}

func TestGet_UnknownIDIsNotFound(t *testing.T) {
	if _, ok := Get("does-not-exist"); ok {
		t.Fatal("Get(\"does-not-exist\") unexpectedly found")
	}
}

// TestEveryRecipesMockPayloadIsShapedLikeMockDTO parses every non-nil Mock
// field as dto.MockDTO, so a wrong-shaped payload fails here, not just at BDD runtime.
func TestEveryRecipesMockPayloadIsShapedLikeMockDTO(t *testing.T) {
	for _, s := range List("") {
		r, ok := Get(s.ID)
		if !ok {
			t.Fatalf("Get(%q) not found after List returned it", s.ID)
		}
		if r.Mock == nil {
			continue
		}
		var parsed dto.MockDTO
		if err := json.Unmarshal(r.Mock, &parsed); err != nil {
			t.Errorf("recipe %q: mock payload does not decode as dto.MockDTO: %v", s.ID, err)
			continue
		}
		if parsed.Name == "" {
			t.Errorf("recipe %q: mock payload has no name", s.ID)
		}
		if parsed.Match.Method == "" || parsed.Match.Path == "" {
			t.Errorf("recipe %q: mock payload has no method/path match condition", s.ID)
		}
		if parsed.Action.Respond == nil {
			t.Errorf("recipe %q: mock payload has no respond action", s.ID)
		}
	}
}
