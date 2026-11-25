package gcpfake

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"
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

const runQueryPath = "/v1/projects/p-1234/databases/(default)/documents:runQuery"

// fakeQueryIDs posts a structured query and returns the document IDs the fake
// answered, in order.
func fakeQueryIDs(t *testing.T, f *Firestore, q string) []string {
	t.Helper()
	code, body, _ := rtdbDo(t, "POST", f.URL+runQueryPath, q, nil)
	if code != 200 {
		t.Fatalf("query = %d %s", code, body)
	}
	var rows []struct {
		Document *struct {
			Name string `json:"name"`
		} `json:"document"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("%v in %s", err, body)
	}
	var ids []string
	for _, r := range rows {
		if r.Document != nil {
			ids = append(ids, path.Base(r.Document.Name))
		}
	}
	return ids
}

// TestFakeRunQueryMatchesOrder runs the query a page of the run index is: an
// equality filter, an order by of two fields, a limit and an exclusive startAt
// cursor.
func TestFakeRunQueryMatchesOrder(t *testing.T) {
	f := NewFirestore(t)
	for _, d := range []struct{ id, plan, at string }{
		{"run-a", "alpha", "2026-11-20T10:00:00Z"},
		{"run-b", "alpha", "2026-11-22T10:00:00Z"},
		{"run-c", "alpha", "2026-11-22T10:00:00Z"},
		{"run-d", "beta", "2026-11-23T10:00:00Z"},
	} {
		ts, err := time.Parse(time.RFC3339, d.at)
		if err != nil {
			t.Fatal(err)
		}
		f.Set("runIndex", d.id, map[string]any{"plan": d.plan, "updated_at": ts})
	}
	query := func(order string, limit int, cursor string) string {
		return fmt.Sprintf(`{"structuredQuery":{"from":[{"collectionId":"runIndex"}],
			"where":{"fieldFilter":{"field":{"fieldPath":"plan"},"op":"EQUAL","value":{"stringValue":"alpha"}}},
			"orderBy":%s,"limit":%d%s}}`, order, limit, cursor)
	}
	// The query names no __name__ clause, as the client sends none: the fake's
	// own implicit tie-break orders the ties, in the sort's direction.
	const desc = `[{"field":{"fieldPath":"updated_at"},"direction":"DESCENDING"}]`

	// Newest first; the tie on updated_at is broken by the run id, descending
	// with the sort.
	if ids := fakeQueryIDs(t, f, query(desc, 2, "")); !reflect.DeepEqual(ids, []string{"run-c", "run-b"}) {
		t.Fatalf("first page = %v", ids)
	}
	// The page after it reads on from the last row: a startAt with before
	// false is exclusive, so run-b is not answered twice.
	after := `,"startAt":{"values":[{"timestampValue":"2026-11-22T10:00:00.000000Z"},` +
		`{"referenceValue":"projects/p-1234/databases/(default)/documents/runIndex/run-b"}],"before":false}`
	if ids := fakeQueryIDs(t, f, query(desc, 2, after)); !reflect.DeepEqual(ids, []string{"run-a"}) {
		t.Fatalf("second page = %v", ids)
	}
	if ids := fakeQueryIDs(t, f, query(`[{"field":{"fieldPath":"updated_at"}}]`, 10, "")); !reflect.DeepEqual(ids, []string{"run-a", "run-b", "run-c"}) {
		t.Fatalf("ascending = %v", ids)
	}

	// Two clauses with the LAST one descending: the implicit tie-break follows
	// that clause's direction, not the first clause's and not the ascending
	// default. plan ascending (all equal here), updated_at descending, and the
	// tie on updated_at broken by the run id descending with it.
	if ids := fakeQueryIDs(t, f, query(`[{"field":{"fieldPath":"plan"}},{"field":{"fieldPath":"updated_at"},"direction":"DESCENDING"}]`, 10, "")); !reflect.DeepEqual(ids, []string{"run-c", "run-b", "run-a"}) {
		t.Fatalf("two clauses, the last descending = %v", ids)
	}

	for name, q := range map[string]string{
		"short cursor":  query(desc, 2, `,"startAt":{"values":[{"timestampValue":"2026-11-22T10:00:00.000000Z"}],"before":false}`),
		"bad direction": query(`[{"field":{"fieldPath":"updated_at"},"direction":"SIDEWAYS"}]`, 2, ""),
	} {
		if code, b, _ := rtdbDo(t, "POST", f.URL+runQueryPath, q, nil); code != 400 {
			t.Errorf("%s = %d %s", name, code, b)
		}
	}
}
