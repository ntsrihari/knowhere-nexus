// Package contract validates the versioned Nexus data contracts
// (docs/CONTRACTS.md): knowhere.catalog and knowhere.theme.
package contract

import (
	"fmt"
	"strings"
)

// Error codes.
const (
	CodeTooLarge           = "too_large"
	CodeInvalidDocument    = "invalid_document"
	CodeWrongSchema        = "wrong_schema"
	CodeUnsupportedVersion = "unsupported_schema_version"
	CodeSchemaViolation    = "schema_violation"
	CodeInvalidValue       = "invalid_value"
	CodeStaleRevision      = "stale_revision"
)

// maxProblems bounds the number of problems reported.
const maxProblems = 20

// Error is a contract validation failure.
type Error struct {
	Code     string
	Problems []string
}

func (e *Error) Error() string {
	if len(e.Problems) == 0 {
		return e.Code
	}
	return e.Code + ": " + strings.Join(e.Problems, "; ")
}

func newErr(code string, format string, a ...any) *Error {
	return &Error{Code: code, Problems: []string{fmt.Sprintf(format, a...)}}
}
