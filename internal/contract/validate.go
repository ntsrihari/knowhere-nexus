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

// validateDocument applies the shared pipeline: schema name, version mode
// (R-17), JSON Schema validation, then decoding into out.
func validateDocument(value any, schemaName, schemaFile string, sup support, out any) error {
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
	sch := schemas[schemaFile]

	raw, err := json.Marshal(value)
	if err != nil {
		return newErr(CodeInvalidDocument, "cannot encode document")
	}
	if md == lenient {
		// Newer MINOR: decode leniently (unknown fields dropped), then
		// validate what Nexus understands.
		if err := json.Unmarshal(raw, out); err != nil {
			return newErr(CodeSchemaViolation, "%v", err)
		}
		pruned, err := json.Marshal(out)
		if err != nil {
			return newErr(CodeInvalidDocument, "cannot encode document")
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(pruned))
		if err != nil {
			return newErr(CodeInvalidDocument, "cannot decode document")
		}
		return schemaCheck(sch, inst)
	}

	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return newErr(CodeInvalidDocument, "cannot decode document")
	}
	if err := schemaCheck(sch, inst); err != nil {
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
