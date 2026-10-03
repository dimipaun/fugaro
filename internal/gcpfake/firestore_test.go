package gcpfake

import (
	"net/url"
	"strings"
	"testing"
)

const fsDocs = "/v1/projects/p-1234/databases/(default)/documents/"

func TestFakeRejectsBadMask(t *testing.T) {
	f := NewFirestore(t)
	body := `{"fields":{"a":{"integerValue":"1"}}}`
	for _, mask := range []string{"by-model", "a b", "`a", "a.", ".a", "", "`a`b", "a..b", "``", "1a"} {
		u := f.URL + fsDocs + "c/x?updateMask.fieldPaths=" + url.QueryEscape(mask)
		if code, b, _ := rtdbDo(t, "PATCH", u, body, nil); code != 400 || !strings.Contains(b, "INVALID_ARGUMENT") {
			t.Errorf("mask %q = %d %s", mask, code, b)
		}
	}
	if _, ok := f.Value("c", "x"); ok {
		t.Fatal("a refused write was stored")
	}
	for _, mask := range []string{"a", "`by-model`", "`a\\`b`", "a.b", "a.`b-c`", "_x1"} {
		u := f.URL + fsDocs + "c/x?updateMask.fieldPaths=" + url.QueryEscape(mask)
		if code, b, _ := rtdbDo(t, "PATCH", u, body, nil); code != 200 {
			t.Errorf("mask %q = %d %s", mask, code, b)
		}
	}
}

func TestFakeRejectsBadValuesAndNames(t *testing.T) {
	f := NewFirestore(t)
	for name, tc := range map[string]struct{ path, body string }{
		"integer as number": {"c/x", `{"fields":{"a":{"integerValue":1}}}`},
		"integer not int64": {"c/x", `{"fields":{"a":{"integerValue":"1.5"}}}`},
		"two kinds":         {"c/x", `{"fields":{"a":{"integerValue":"1","stringValue":"x"}}}`},
		"plain json":        {"c/x", `{"fields":{"a":1}}`},
		"bad timestamp":     {"c/x", `{"fields":{"a":{"timestampValue":"yesterday"}}}`},
		"empty map key":     {"c/x", `{"fields":{"a":{"mapValue":{"fields":{"":{"nullValue":null}}}}}}`},
		"wrong name":        {"c/x", `{"name":"projects/p-1234/databases/(default)/documents/c/y","fields":{}}`},
		"nested path":       {"c/x/d/y", `{"fields":{}}`},
		"dunder id":         {"c/__x__", `{"fields":{}}`},
		"dot id":            {"c/..", `{"fields":{}}`},
	} {
		if code, b, _ := rtdbDo(t, "PATCH", f.URL+fsDocs+tc.path, tc.body, nil); code != 400 {
			t.Errorf("%s = %d %s", name, code, b)
		}
	}
}

func TestFakeNoMaskReplacesDocument(t *testing.T) {
	f := NewFirestore(t)
	f.Set("c", "x", map[string]any{"a": "1", "b": "2"})
	if code, _, _ := rtdbDo(t, "PATCH", f.URL+fsDocs+"c/x", `{"fields":{"a":{"stringValue":"9"}}}`, nil); code != 200 {
		t.Fatal(code)
	}
	if v, _ := f.Value("c", "x"); len(v) != 1 || v["a"] != "9" {
		t.Fatalf("v = %v", v)
	}
}

func TestFakeRejectsIndexedQuery(t *testing.T) {
	f := NewFirestore(t)
	q := `{"structuredQuery":{"from":[{"collectionId":"c"}],"where":{"compositeFilter":{"op":"AND","filters":[
	 {"fieldFilter":{"field":{"fieldPath":"date"},"op":"GREATER_THAN_OR_EQUAL","value":{"stringValue":"a"}}},
	 {"fieldFilter":{"field":{"fieldPath":"repo"},"op":"EQUAL","value":{"stringValue":"r"}}}]}}}}`
	code, b, _ := rtdbDo(t, "POST", f.URL+"/v1/projects/p-1234/databases/(default)/documents:runQuery", q, nil)
	if code != 400 || !strings.Contains(b, "index") {
		t.Fatalf("= %d %s", code, b)
	}
	// An inequality ordered by another field first is refused too.
	q = `{"structuredQuery":{"from":[{"collectionId":"c"}],"where":{"fieldFilter":{"field":{"fieldPath":"date"},"op":"LESS_THAN","value":{"stringValue":"a"}}},
	 "orderBy":[{"field":{"fieldPath":"repo"}}]}}`
	if code, _, _ := rtdbDo(t, "POST", f.URL+"/v1/projects/p-1234/databases/(default)/documents:runQuery", q, nil); code != 400 {
		t.Fatal(code)
	}
}
