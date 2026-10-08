package jsonpath

import (
	"encoding/json"
	"mime"
	"net/textproto"
	"net/url"
)

// formMediaType is the only media type Document and FormFields treat as a
// form. Lyrebird never guesses from the bytes: text that merely looks like
// "a=b" is not a form unless the sender declared it as one.
const formMediaType = "application/x-www-form-urlencoded"

// Document returns the bytes a body path should be evaluated against for a
// message carrying headers and body.
//
// JSON — and every body that is neither JSON nor a declared form — is returned
// verbatim, so existing JSON lookups behave exactly as before. A body declared
// application/x-www-form-urlencoded that is not valid JSON is returned as a
// flat JSON object of key -> first value (string), so the same path syntax
// ("text", "$.channel") addresses form fields. A declared form that does not
// decode returns nil, which gjson reports as "every path absent": it fails
// closed rather than half-parsing.
//
// Note that gjson treats "." as a path separator, so a form key that itself
// contains a dot ("user.name=x") must be addressed escaped ("user\.name").
func Document(headers map[string][]string, body []byte) []byte {
	fields, isForm := FormFields(headers, body)
	if !isForm {
		return body
	}
	if fields == nil {
		return nil
	}
	doc, err := json.Marshal(fields)
	if err != nil {
		return nil
	}
	return doc
}

// FormFields decodes body as a form when headers declare it one and it is not
// valid JSON. isForm reports whether the body is treated as a form at all;
// fields is nil when it is but fails to decode (or is empty). Repeated keys
// keep their first value, the same way query-string conditions read one.
func FormFields(headers map[string][]string, body []byte) (fields map[string]string, isForm bool) {
	if !declaresForm(headers) || json.Valid(body) {
		return nil, false
	}
	values, err := url.ParseQuery(string(body))
	if err != nil || len(values) == 0 {
		return nil, true
	}
	fields = make(map[string]string, len(values))
	for key, vs := range values {
		if len(vs) > 0 {
			fields[key] = vs[0]
		}
	}
	return fields, true
}

func declaresForm(headers map[string][]string) bool {
	values := headers[textproto.CanonicalMIMEHeaderKey("Content-Type")]
	if len(values) == 0 {
		// Callers that did not canonicalize (a hand-built map) still count.
		for key, vs := range headers {
			if textproto.CanonicalMIMEHeaderKey(key) == "Content-Type" {
				values = vs
				break
			}
		}
	}
	if len(values) == 0 {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(values[0])
	return err == nil && mediaType == formMediaType
}
