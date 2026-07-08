package httpadmin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListExamplesReturnsEveryRecipe(t *testing.T) {
	rr := httptest.NewRecorder()
	ListExamples(rr, newGetRequest(t, "/__lyrebird/examples"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	var list []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(list) != 11 {
		t.Errorf("returned %d entries, want 11", len(list))
	}
	for _, entry := range list {
		if _, ok := entry["mock"]; ok {
			t.Errorf("summary %+v unexpectedly includes a mock field", entry)
		}
	}
}

func TestListExamplesFiltersByQuery(t *testing.T) {
	rr := httptest.NewRecorder()
	ListExamples(rr, newGetRequest(t, "/__lyrebird/examples?query=aws"))

	var list []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(list) != 5 {
		t.Errorf(`query="aws" returned %d entries, want 5`, len(list))
	}
}

func TestGetExampleReturnsTheFullRecipe(t *testing.T) {
	req := newGetRequest(t, "/__lyrebird/examples/aws-sns")
	req.SetPathValue("id", "aws-sns")
	rr := httptest.NewRecorder()
	GetExample(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if doc["mock"] == nil {
		t.Error("mock field is nil, want the recipe's ready-to-adapt payload")
	}
}

func TestGetExampleUnknownIDIs404(t *testing.T) {
	req := newGetRequest(t, "/__lyrebird/examples/does-not-exist")
	req.SetPathValue("id", "does-not-exist")
	rr := httptest.NewRecorder()
	GetExample(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}
