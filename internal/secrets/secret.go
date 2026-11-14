// Package secrets loads the Nexus secrets file and provides a Secret type that
// cannot be printed, logged or serialised by accident.
package secrets

import (
	"fmt"
	"log/slog"
)

// Redacted is what every formatting path prints instead of a secret value.
const Redacted = "[redacted]"

// Secret holds sensitive bytes. Every fmt verb, JSON and text marshalling and
// slog output yields Redacted. Use Expose only at the point of use.
type Secret struct {
	v []byte
}

// New wraps b (copied).
func New(b []byte) Secret { return Secret{v: append([]byte(nil), b...)} }

// Expose returns a copy of the secret bytes. Callers must not log or return it.
func (s Secret) Expose() []byte { return append([]byte(nil), s.v...) }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return len(s.v) == 0 }

// String implements fmt.Stringer.
func (Secret) String() string { return Redacted }

// GoString implements fmt.GoStringer.
func (Secret) GoString() string { return Redacted }

// Format implements fmt.Formatter so that every verb (%v %+v %#v %s %q %x …)
// is redacted.
func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(Redacted)) }

// MarshalJSON implements json.Marshaler.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// MarshalText implements encoding.TextMarshaler.
func (Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }
