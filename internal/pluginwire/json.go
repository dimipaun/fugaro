package pluginwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// member is one key of a JSON object with its value kept verbatim.
type member struct {
	key string
	val json.RawMessage
}

// object is a JSON object that keeps its key order, so a merge changes only
// the keys it means to change.
type object []member

// parseObject parses raw, which must be one JSON object and nothing else.
// A duplicate key is refused: which of two values Claude Code honors is not
// ours to guess, so the file is left for a person to fix.
func parseObject(raw []byte) (object, error) {
	if !json.Valid(raw) {
		// Say why: Valid alone doesn't. Decode reports the offset and cause.
		var v any
		err := json.Unmarshal(raw, &v)
		return nil, fmt.Errorf("not valid JSON (comments and trailing commas are not accepted): %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("the top level is not a JSON object")
	}
	var o object
	seen := map[string]bool{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k := kt.(string)
		if seen[k] {
			return nil, fmt.Errorf("the key %q appears twice", k)
		}
		seen[k] = true
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = append(o, member{k, v})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("data after the JSON object")
	}
	return o, nil
}

func (o object) index(key string) int {
	for i, m := range o {
		if m.key == key {
			return i
		}
	}
	return -1
}

// get returns the value of key.
func (o object) get(key string) (json.RawMessage, bool) {
	if i := o.index(key); i >= 0 {
		return o[i].val, true
	}
	return nil, false
}

// set replaces key's value in place, or appends the key. It reports whether
// the object changed semantically.
func (o *object) set(key string, val json.RawMessage) bool {
	if i := o.index(key); i >= 0 {
		if jsonEqual((*o)[i].val, val) {
			return false
		}
		(*o)[i].val = val
		return true
	}
	*o = append(*o, member{key, val})
	return true
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ab, _ := json.Marshal(x)
	bb, _ := json.Marshal(y)
	return bytes.Equal(ab, bb)
}

// raw renders o as compact JSON.
func (o object) raw() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(m.key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// objectAt reads key of o as an object. A missing key is an empty object
// with present false; a value that is not an object is an error naming key.
func objectAt(o object, key string) (sub object, present bool, err error) {
	raw, ok := o.get(key)
	if !ok {
		return nil, false, nil
	}
	sub, err = parseObject(raw)
	if err != nil {
		return nil, true, fmt.Errorf("%s is not a JSON object: %w", key, err)
	}
	return sub, true, nil
}

func marshalString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// pretty renders o with two-space indentation and a final newline.
func pretty(o object) []byte {
	var b bytes.Buffer
	_ = json.Indent(&b, o.raw(), "", "  ")
	b.WriteByte('\n')
	return b.Bytes()
}
