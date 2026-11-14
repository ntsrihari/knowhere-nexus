package safeyaml

import (
	"reflect"
	"strings"
	"testing"
)

func TestRejects(t *testing.T) {
	cases := map[string]string{
		"anchor":         "a: &x 1\nb: 2\n",
		"alias":          "a: &x [1]\nb: *x\n",
		"merge":          "base: {x: 1}\nchild:\n  <<: {y: 2}\n",
		"duplicate":      "a: 1\na: 2\n",
		"nested dup":     "a:\n  b: 1\n  b: 2\n",
		"non-string key": "1: one\n",
		"multi-document": "a: 1\n---\nb: 2\n",
		"empty":          "",
		"explicit tag":   "a: !!binary aGVsbG8=\n",
		"custom tag":     "a: !custom x\n",
		"billion laughs": "a: &a [x,x,x]\nb: &b [*a,*a,*a]\nc: [*b,*b,*b]\n",
		"syntax error":   "a: [1, 2\n",
		"too deep":       strings.Repeat("[", MaxDepth+2) + strings.Repeat("]", MaxDepth+2) + "\n",
	}
	for name, doc := range cases {
		if _, err := ToValue([]byte(doc)); err == nil {
			t.Errorf("%s: ToValue accepted %q", name, doc)
		}
	}
}

func TestToValueTypes(t *testing.T) {
	doc := "s: hi\ni: 42\nf: 1.5\nb: true\nn: null\nts: 2026-11-14T00:00:00Z\nlist: [1, two]\nm: {k: v}\n"
	got, err := ToValue([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"s": "hi", "i": int64(42), "f": 1.5, "b": true, "n": nil,
		"ts":   "2026-11-14T00:00:00Z",
		"list": []any{int64(1), "two"},
		"m":    map[string]any{"k": "v"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestDecodeStrict(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
		Port int    `yaml:"port"`
	}
	var c cfg
	if err := DecodeStrict([]byte("name: x\nport: 1\n"), &c); err != nil || c.Name != "x" || c.Port != 1 {
		t.Fatalf("valid: %v %+v", err, c)
	}
	for name, doc := range map[string]string{
		"unknown key":   "name: x\nextra: 1\n",
		"type mismatch": "name: x\nport: notanumber\n",
		"duplicate":     "name: x\nname: y\n",
		"alias":         "name: &n x\nport: 1\nother: *n\n",
	} {
		var c cfg
		if err := DecodeStrict([]byte(doc), &c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
