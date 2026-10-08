package jsonpath

import "testing"

func formHeaders() map[string][]string {
	return map[string][]string{"Content-Type": {"application/x-www-form-urlencoded; charset=utf-8"}}
}

func TestDocument_FormBodyBecomesFlatObject(t *testing.T) {
	doc := Document(formHeaders(), []byte("channel=C1&text=hello+world&text=second"))
	if got := GetBytes(doc, "text").String(); got != "hello world" {
		t.Fatalf("text = %q, want first value %q", got, "hello world")
	}
	if got := GetBytes(doc, "$.channel").String(); got != "C1" {
		t.Fatalf("$.channel = %q, want C1", got)
	}
}

func TestDocument_JSONBodyUnchangedEvenWhenDeclaredForm(t *testing.T) {
	body := []byte(`{"a":{"b":1}}`)
	for _, h := range []map[string][]string{nil, formHeaders(), {"Content-Type": {"application/json"}}} {
		if got := Document(h, body); string(got) != string(body) {
			t.Fatalf("Document(%v) = %s, want body verbatim", h, got)
		}
	}
}

func TestDocument_NonFormTextIsNotGuessed(t *testing.T) {
	body := []byte("text=hello")
	for _, h := range []map[string][]string{nil, {"Content-Type": {"text/plain"}}} {
		if got := GetBytes(Document(h, body), "text"); got.Exists() {
			t.Fatalf("Document(%v): text exists = %q, want absent for an undeclared form", h, got.String())
		}
	}
}

func TestDocument_UndecodableFormFailsClosed(t *testing.T) {
	if got := Document(formHeaders(), []byte("text=%zz")); got != nil {
		t.Fatalf("Document = %s, want nil", got)
	}
}

func TestDocument_LowercaseHeaderKey(t *testing.T) {
	h := map[string][]string{"content-type": {"application/x-www-form-urlencoded"}}
	if got := GetBytes(Document(h, []byte("a=1")), "a").String(); got != "1" {
		t.Fatalf("a = %q, want 1", got)
	}
}

func TestFormFields(t *testing.T) {
	fields, isForm := FormFields(formHeaders(), []byte("a=1&b=x+y"))
	if !isForm || fields["a"] != "1" || fields["b"] != "x y" {
		t.Fatalf("FormFields = %v, %v", fields, isForm)
	}
	if _, isForm := FormFields(nil, []byte("a=1")); isForm {
		t.Fatal("undeclared body treated as form")
	}
	if fields, isForm := FormFields(formHeaders(), nil); !isForm || fields != nil {
		t.Fatalf("empty form = %v, %v; want nil, true", fields, isForm)
	}
}
