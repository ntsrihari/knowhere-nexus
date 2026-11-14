package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxJSONDepth = 32

// decodeJSONValue decodes a single JSON value into plain Go values
// (map[string]any, []any, string, int64, float64, bool, nil), rejecting
// duplicate object keys, excessive nesting and trailing data.
func decodeJSONValue(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := readValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after JSON value")
	}
	return v, nil
}

func readValue(dec *json.Decoder, depth int) (any, error) {
	if depth > maxJSONDepth {
		return nil, errors.New("JSON nesting too deep")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]any{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("invalid JSON: %v", err)
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("invalid JSON object key")
				}
				if _, dup := m[k]; dup {
					return nil, fmt.Errorf("duplicate key %q", k)
				}
				v, err := readValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				m[k] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, fmt.Errorf("invalid JSON: %v", err)
			}
			return m, nil
		case '[':
			s := []any{}
			for dec.More() {
				v, err := readValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				s = append(s, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, fmt.Errorf("invalid JSON: %v", err)
			}
			return s, nil
		}
		return nil, errors.New("invalid JSON")
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, nil
		}
		f, err := t.Float64()
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", t.String())
		}
		return f, nil
	default: // string, bool, nil
		return t, nil
	}
}
