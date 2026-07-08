package jsonpath

import "testing"

func TestGetBytesAcceptsBareAndDollarPrefixedPaths(t *testing.T) {
	body := []byte(`{"amount":"666","nested":{"field":"value"}}`)
	cases := map[string]string{
		"amount":         "666",
		"$.amount":       "666",
		"nested.field":   "value",
		"$.nested.field": "value",
	}
	for path, want := range cases {
		got := GetBytes(body, path)
		if !got.Exists() {
			t.Errorf("GetBytes(%q) does not exist, want %q", path, want)
			continue
		}
		if got.String() != want {
			t.Errorf("GetBytes(%q) = %q, want %q", path, got.String(), want)
		}
	}
}

func TestGetBytesRootMarkerReturnsWholeDocument(t *testing.T) {
	body := []byte(`{"a":1}`)
	got := GetBytes(body, "$")
	if !got.Exists() {
		t.Fatal("GetBytes($) does not exist")
	}
	if got.String() != `{"a":1}` {
		t.Errorf("GetBytes($) = %q, want the whole document", got.String())
	}
}

func TestGetBytesMissingPathDoesNotExist(t *testing.T) {
	body := []byte(`{"amount":"666"}`)
	if GetBytes(body, "$.missing").Exists() {
		t.Error("GetBytes($.missing) exists, want absent")
	}
}
