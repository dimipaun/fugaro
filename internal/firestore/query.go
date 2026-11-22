package firestore

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
)

// Order is one clause of a Query's orderBy: a top-level field and its
// direction.
type Order struct {
	Field     string
	Direction string // "ASCENDING" (the default) or "DESCENDING"
}

// Query is a structured query: equality filters, an orderBy of at most two
// fields, a limit and a startAfter cursor. It is the shape a page of the run
// index needs (plan RI1, finding F10): one plan's or repository's runs, newest
// first, one page at a time.
//
// __name__ is appended to the orderBy when the caller names no field for it,
// as the SDKs send it, so the order is total and a cursor over it is stable:
// a tie in the last ordered field is broken by the document ID, ascending.
type Query struct {
	Collection string
	Where      map[string]any // equality filters, field name -> value
	OrderBy    []Order        // one or two fields, in order
	Limit      int            // documents to return; 0 is the client's page size
	StartAfter *Doc           // read on from this document; nil starts at the first
}

// RunQuery runs one structured query and returns its documents in the query's
// order, at most Limit of them. It makes exactly one request, so a page costs
// the same whatever the history behind it: the caller that wants the next page
// passes the last document it read back as StartAfter.
func (c *Client) RunQuery(ctx context.Context, q Query) ([]Doc, error) {
	if !validName(q.Collection) {
		return nil, fmt.Errorf("firestore: %q is not a collection", q.Collection)
	}
	if q.Limit < 0 {
		return nil, errors.New("firestore: a query limit cannot be negative")
	}
	if len(q.OrderBy) > 2 {
		return nil, fmt.Errorf("firestore: a query orders by at most two fields, not %d", len(q.OrderBy))
	}
	limit := q.Limit
	if limit == 0 {
		limit = c.pageSize
	}
	order := make([]any, 0, len(q.OrderBy)+1)
	fields := make([]string, 0, len(q.OrderBy)+1)
	for _, o := range q.OrderBy {
		if o.Field == "" {
			return nil, errors.New("firestore: an order field is empty")
		}
		switch o.Direction {
		case "", "ASCENDING", "DESCENDING":
		default:
			return nil, fmt.Errorf("firestore: %q is not an order direction", o.Direction)
		}
		order = append(order, map[string]any{
			"field":     map[string]any{"fieldPath": FieldPath(o.Field)},
			"direction": o.Direction,
		})
		fields = append(fields, o.Field)
	}
	if len(fields) == 0 || fields[len(fields)-1] != "__name__" {
		order = append(order, map[string]any{
			"field":     map[string]any{"fieldPath": "__name__"},
			"direction": "ASCENDING",
		})
		fields = append(fields, "__name__")
	}
	sq := map[string]any{
		"from":    []any{map[string]any{"collectionId": q.Collection}},
		"orderBy": order,
		"limit":   limit,
	}
	filters, err := equalityFilters(q.Where)
	if err != nil {
		return nil, err
	}
	switch len(filters) {
	case 0:
	case 1:
		sq["where"] = filters[0]
	default:
		sq["where"] = map[string]any{"compositeFilter": map[string]any{"op": "AND", "filters": filters}}
	}
	if q.StartAfter != nil {
		values, err := c.cursorValues(q.StartAfter, fields)
		if err != nil {
			return nil, err
		}
		sq["startAfter"] = map[string]any{"values": values}
	}
	var rows []struct {
		Document *rawDoc `json:"document"`
	}
	if err := c.do(ctx, "QUERY", q.Collection, http.MethodPost, "/v1/"+c.dbPath()+"/documents:runQuery", nil, map[string]any{"structuredQuery": sq}, &rows); err != nil {
		return nil, err
	}
	out := make([]Doc, 0, len(rows))
	for _, r := range rows {
		if r.Document == nil {
			continue // the readTime-only row of an empty result
		}
		d, err := r.Document.decode()
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, nil
}

// equalityFilters renders a query's filters in field order, so the same query
// always sends the same bytes.
func equalityFilters(where map[string]any) ([]any, error) {
	names := make([]string, 0, len(where))
	for k := range where {
		names = append(names, k)
	}
	sort.Strings(names)
	filters := make([]any, 0, len(names))
	for _, k := range names {
		if k == "" {
			return nil, errors.New("firestore: a filter field is empty")
		}
		v, err := Encode(where[k])
		if err != nil {
			return nil, fmt.Errorf("firestore: filter %q: %w", k, err)
		}
		filters = append(filters, map[string]any{"fieldFilter": map[string]any{
			"field": map[string]any{"fieldPath": FieldPath(k)}, "op": "EQUAL", "value": v}})
	}
	return filters, nil
}

// cursorValues is the startAfter cursor for a query ordered by fields: one
// value per ordered field, taken from doc, then doc's own resource name (the
// __name__ the query orders by last).
func (c *Client) cursorValues(doc *Doc, fields []string) ([]any, error) {
	ref, err := c.docPath(doc.Collection, doc.ID)
	if err != nil {
		return nil, err
	}
	values := make([]any, 0, len(fields))
	for _, f := range fields {
		if f == "__name__" {
			values = append(values, map[string]any{"referenceValue": ref})
			continue
		}
		v, ok := doc.Fields[f]
		if !ok {
			return nil, fmt.Errorf("firestore: the cursor document %s/%s has no field %q to order by", doc.Collection, doc.ID, f)
		}
		ev, err := Encode(v)
		if err != nil {
			return nil, err
		}
		values = append(values, ev)
	}
	return values, nil
}
