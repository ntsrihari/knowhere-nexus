package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/ntsrihari/knowhere-nexus/contracts"
)

var (
	schemaOnce sync.Once
	schemaErr  error
	schemas    = map[string]*jsonschema.Schema{}
)

// loadSchemas compiles the embedded schemas once. A failure is a build
// defect, surfaced on first use and by tests.
func loadSchemas() error {
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.AssertFormat()
		for _, name := range []string{contracts.CatalogSchemaFile, contracts.ThemeSchemaFile} {
			b, err := contracts.Schemas.ReadFile(name)
			if err != nil {
				schemaErr = err
				return
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
			if err != nil {
				schemaErr = fmt.Errorf("%s: %w", name, err)
				return
			}
			if err := c.AddResource(name, doc); err != nil {
				schemaErr = fmt.Errorf("%s: %w", name, err)
				return
			}
		}
		for _, name := range []string{contracts.CatalogSchemaFile, contracts.ThemeSchemaFile} {
			s, err := c.Compile(name)
			if err != nil {
				schemaErr = fmt.Errorf("%s: %w", name, err)
				return
			}
			schemas[name] = s
		}
	})
	return schemaErr
}

// SelfCheck compiles every embedded schema; used at startup.
func SelfCheck() error { return loadSchemas() }

// fieldSet describes the known (exact-case) fields of an object and, for
// nested objects, how to prune them. Used only for newer-MINOR documents.
type fieldSet struct {
	fields map[string]*fieldSet // nil value: keep as-is (scalar or opaque)
	items  *fieldSet            // for arrays of objects
}

// prune drops unknown keys (exact-case match only) from v following fs.
func prune(v any, fs *fieldSet) any {
	if fs == nil {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		if fs.fields == nil {
			return t
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			sub, known := fs.fields[k]
			if !known {
				continue
			}
			out[k] = prune(val, sub)
		}
		return out
	case []any:
		if fs.items == nil {
			return t
		}
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = prune(val, fs.items)
		}
		return out
	default:
		return v
	}
}

func keep(names ...string) map[string]*fieldSet {
	m := make(map[string]*fieldSet, len(names))
	for _, n := range names {
		m[n] = nil
	}
	return m
}

// validateDocument applies the shared pipeline: schema name, version mode
// (R-17), JSON Schema validation, then strict decoding into out.
// A newer-MINOR document first has its unknown fields removed (exact-case);
// everything Nexus knows about is then validated exactly as for a known
// version, so required fields and types are never relaxed.
func validateDocument(value any, schemaName, schemaFile string, sup support, known *fieldSet, out any) error {
	m, ok := value.(map[string]any)
	if !ok {
		return newErr(CodeInvalidDocument, "document must be an object")
	}
	if got, _ := m["schema"].(string); got != schemaName {
		return newErr(CodeWrongSchema, "schema must be %q", schemaName)
	}
	vs, _ := m["schemaVersion"].(string)
	ver, err := ParseVersion(vs)
	if err != nil {
		return newErr(CodeUnsupportedVersion, "%v", err)
	}
	md, err := sup.modeFor(ver)
	if err != nil {
		return err
	}
	if err := loadSchemas(); err != nil {
		return fmt.Errorf("contract schemas: %w", err)
	}
	if md == lenient {
		value = prune(value, known)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return newErr(CodeInvalidDocument, "cannot encode document")
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return newErr(CodeInvalidDocument, "cannot decode document")
	}
	if err := schemaCheck(schemas[schemaFile], inst); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return newErr(CodeSchemaViolation, "%v", err)
	}
	return nil
}

func schemaCheck(sch *jsonschema.Schema, inst any) error {
	err := sch.Validate(inst)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return newErr(CodeSchemaViolation, "%v", err)
	}
	e := &Error{Code: CodeSchemaViolation}
	collectLeaves(ve, e)
	if len(e.Problems) == 0 {
		e.Problems = []string{strings.TrimSpace(ve.Error())}
	}
	return e
}

func collectLeaves(ve *jsonschema.ValidationError, e *Error) {
	if len(e.Problems) >= maxProblems {
		return
	}
	if len(ve.Causes) == 0 {
		loc := "/" + strings.Join(ve.InstanceLocation, "/")
		e.Problems = append(e.Problems, fmt.Sprintf("%s: %s", loc, ve.ErrorKind.LocalizedString(printer)))
		return
	}
	for _, c := range ve.Causes {
		collectLeaves(c, e)
	}
}
