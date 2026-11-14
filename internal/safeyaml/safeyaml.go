// Package safeyaml decodes YAML defensively. It accepts a single document and
// rejects anchors, aliases, merge keys, duplicate mapping keys, non-string
// keys and explicit custom tags. These restrictions block alias-expansion
// ("billion laughs") attacks and remove ambiguity from configuration and
// contract files.
package safeyaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"

	"gopkg.in/yaml.v3"
)

// MaxDepth bounds nesting of mappings and sequences.
const MaxDepth = 32

// Error describes why a document was rejected.
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("yaml line %d: %s", e.Line, e.Msg)
	}
	return "yaml: " + e.Msg
}

// parse returns the root node of exactly one document after structural checks.
func parse(data []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, &Error{Msg: "empty document"}
		}
		return nil, &Error{Msg: err.Error()}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, &Error{Msg: "multiple documents are not allowed"}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, &Error{Msg: "expected a single document"}
	}
	if err := check(doc.Content[0], 0); err != nil {
		return nil, err
	}
	return doc.Content[0], nil
}

func check(n *yaml.Node, depth int) error {
	if depth > MaxDepth {
		return &Error{Line: n.Line, Msg: "nesting too deep"}
	}
	if n.Anchor != "" {
		return &Error{Line: n.Line, Msg: "anchors are not allowed"}
	}
	switch n.Kind {
	case yaml.AliasNode:
		return &Error{Line: n.Line, Msg: "aliases are not allowed"}
	case yaml.ScalarNode:
		if n.Style&yaml.TaggedStyle != 0 {
			return &Error{Line: n.Line, Msg: "explicit tags are not allowed"}
		}
		return nil
	case yaml.SequenceNode:
		if n.Style&yaml.TaggedStyle != 0 {
			return &Error{Line: n.Line, Msg: "explicit tags are not allowed"}
		}
		for _, c := range n.Content {
			if err := check(c, depth+1); err != nil {
				return err
			}
		}
		return nil
	case yaml.MappingNode:
		if n.Style&yaml.TaggedStyle != 0 {
			return &Error{Line: n.Line, Msg: "explicit tags are not allowed"}
		}
		seen := make(map[string]struct{}, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
				if k.Kind == yaml.ScalarNode && k.Tag == "!!merge" {
					return &Error{Line: k.Line, Msg: "merge keys are not allowed"}
				}
				return &Error{Line: k.Line, Msg: "mapping keys must be strings"}
			}
			if k.Anchor != "" {
				return &Error{Line: k.Line, Msg: "anchors are not allowed"}
			}
			if _, dup := seen[k.Value]; dup {
				return &Error{Line: k.Line, Msg: fmt.Sprintf("duplicate key %q", k.Value)}
			}
			seen[k.Value] = struct{}{}
			if err := check(v, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return &Error{Line: n.Line, Msg: "unsupported node"}
	}
}

// DecodeStrict decodes data into v. Besides the structural checks, every
// mapping key must correspond to a field of v (unknown keys are errors).
func DecodeStrict(data []byte, v any) error {
	if _, err := parse(data); err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return &Error{Msg: err.Error()}
	}
	return nil
}

// ToValue converts a document into plain Go values suitable for JSON Schema
// validation: map[string]any, []any, string, int64, float64, bool and nil.
// Timestamps are returned as their original string form.
func ToValue(data []byte) (any, error) {
	root, err := parse(data)
	if err != nil {
		return nil, err
	}
	return toValue(root)
}

func toValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := toValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[n.Content[i].Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := toValue(c)
			if err != nil {
				return nil, err
			}
			s = append(s, v)
		}
		return s, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "!!timestamp", "!!binary":
			return n.Value, nil
		case "!!null":
			return nil, nil
		case "!!bool":
			b, err := strconv.ParseBool(n.Value)
			if err != nil {
				var v bool
				if derr := n.Decode(&v); derr != nil {
					return nil, &Error{Line: n.Line, Msg: "invalid boolean"}
				}
				return v, nil
			}
			return b, nil
		case "!!int":
			var v int64
			if err := n.Decode(&v); err != nil {
				return nil, &Error{Line: n.Line, Msg: "invalid integer"}
			}
			return v, nil
		case "!!float":
			var v float64
			if err := n.Decode(&v); err != nil {
				return nil, &Error{Line: n.Line, Msg: "invalid number"}
			}
			return v, nil
		default:
			return nil, &Error{Line: n.Line, Msg: "unsupported scalar type " + n.Tag}
		}
	default:
		return nil, &Error{Line: n.Line, Msg: "unsupported node"}
	}
}
