package firestore

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

// Encode turns a Go value into a Firestore typed Value (the JSON shape of
// google.firestore.v1.Value). Supported: nil, string, bool, the integer kinds
// (sent as a decimal string, as REST requires for int64), float64 (not NaN or
// infinite), time.Time (UTC, microseconds), map[string]any and []any.
func Encode(v any) (map[string]any, error) {
	switch x := v.(type) {
	case nil:
		return map[string]any{"nullValue": nil}, nil
	case string:
		return map[string]any{"stringValue": x}, nil
	case bool:
		return map[string]any{"booleanValue": x}, nil
	case int:
		return intValue(int64(x)), nil
	case int32:
		return intValue(int64(x)), nil
	case int64:
		return intValue(x), nil
	case uint32:
		return intValue(int64(x)), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("firestore: cannot encode %v", x)
		}
		return map[string]any{"doubleValue": x}, nil
	case time.Time:
		return map[string]any{"timestampValue": x.UTC().Format("2006-01-02T15:04:05.000000Z")}, nil
	case map[string]any:
		f, err := EncodeFields(x)
		if err != nil {
			return nil, err
		}
		return map[string]any{"mapValue": map[string]any{"fields": f}}, nil
	case []any:
		vs := make([]any, 0, len(x))
		for _, e := range x {
			ev, err := Encode(e)
			if err != nil {
				return nil, err
			}
			vs = append(vs, ev)
		}
		return map[string]any{"arrayValue": map[string]any{"values": vs}}, nil
	}
	return nil, fmt.Errorf("firestore: cannot encode a %T", v)
}

func intValue(i int64) map[string]any {
	return map[string]any{"integerValue": strconv.FormatInt(i, 10)}
}

// EncodeFields encodes a map of fields.
func EncodeFields(m map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k == "" {
			return nil, fmt.Errorf("firestore: empty field name")
		}
		ev, err := Encode(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		out[k] = ev
	}
	return out, nil
}

// Decode is Encode's inverse: nil, string, bool, int64, float64, time.Time,
// map[string]any, []any. A value of another kind (reference, geopoint, bytes)
// is an error: Fugaro writes none of them.
func Decode(v any) (any, error) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, fmt.Errorf("firestore: %v is not a typed value", v)
	}
	for k, raw := range m {
		switch k {
		case "nullValue":
			return nil, nil
		case "stringValue":
			if s, ok := raw.(string); ok {
				return s, nil
			}
		case "booleanValue":
			if b, ok := raw.(bool); ok {
				return b, nil
			}
		case "integerValue":
			if s, ok := raw.(string); ok {
				i, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("firestore: integerValue %q: %w", s, err)
				}
				return i, nil
			}
		case "doubleValue":
			switch n := raw.(type) {
			case float64:
				return n, nil
			case string: // "NaN", "Infinity" are strings on the wire
				return nil, fmt.Errorf("firestore: doubleValue %q is not a finite number", n)
			}
		case "timestampValue":
			if s, ok := raw.(string); ok {
				t, err := time.Parse(time.RFC3339Nano, s)
				if err != nil {
					return nil, fmt.Errorf("firestore: timestampValue %q: %w", s, err)
				}
				return t.UTC(), nil
			}
		case "mapValue":
			if mm, ok := raw.(map[string]any); ok {
				f, _ := mm["fields"].(map[string]any)
				return DecodeFields(f)
			}
		case "arrayValue":
			if am, ok := raw.(map[string]any); ok {
				vs, _ := am["values"].([]any)
				out := make([]any, 0, len(vs))
				for _, e := range vs {
					d, err := Decode(e)
					if err != nil {
						return nil, err
					}
					out = append(out, d)
				}
				return out, nil
			}
		default:
			return nil, fmt.Errorf("firestore: unsupported value kind %q", k)
		}
		return nil, fmt.Errorf("firestore: malformed %s", k)
	}
	return nil, nil
}

// DecodeFields decodes a document's fields.
func DecodeFields(f map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(f))
	for k, v := range f {
		d, err := Decode(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		out[k] = d
	}
	return out, nil
}
