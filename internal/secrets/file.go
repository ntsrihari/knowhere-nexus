package secrets

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/ntsrihari/knowhere-nexus/internal/safeyaml"
)

// MaxFileSize bounds the secrets file.
const MaxFileSize = 64 << 10

// Secrets is the validated content of the secrets file.
type Secrets struct {
	// SessionEncryptionKey is a 32-byte key. M1 loads and validates it; it is
	// first used by refresh-token rotation (R-14) in M2.
	SessionEncryptionKey Secret
	// SigningKey is the Ed25519 seed (32 bytes) for service and delegation
	// tokens (R-03). Optional until services are configured.
	SigningKey Secret
	// JellyfinAdminKey is a dedicated Jellyfin API key used only by the admin
	// broker (R-10). Optional.
	JellyfinAdminKey Secret
	// Integrations holds downstream API keys by integration name.
	Integrations map[string]Secret
}

type fileShape struct {
	SessionEncryptionKey string            `yaml:"sessionEncryptionKey"`
	SigningKey           string            `yaml:"signingKey"`
	JellyfinAdminKey     string            `yaml:"jellyfinAdminKey"`
	Integrations         map[string]string `yaml:"integrations"`
}

// Load reads the secrets file at path. It refuses files that are not regular
// files, that grant any permission to group or others, or that are not owned
// by the effective user running Nexus. Errors never include secret values.
func Load(path string) (*Secrets, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("secrets file %s: symbolic links are not allowed", path)
		}
		return nil, fmt.Errorf("secrets file: %w", err)
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("secrets file: %w", err)
	}
	if err := checkMode(path, st, os.Geteuid()); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("secrets file: %w", err)
	}
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("secrets file %s exceeds %d bytes", path, MaxFileSize)
	}
	return parse(data)
}

func checkMode(path string, st os.FileInfo, euid int) error {
	if !st.Mode().IsRegular() {
		return fmt.Errorf("secrets file %s is not a regular file", path)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("secrets file %s has mode %04o; it must not be accessible by group or others (use 0600 or 0400)", path, perm)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("secrets file %s: cannot determine owner", path)
	}
	if int(sys.Uid) != euid {
		return fmt.Errorf("secrets file %s is owned by uid %d; it must be owned by the Nexus user (uid %d)", path, sys.Uid, euid)
	}
	return nil
}

func parse(data []byte) (*Secrets, error) {
	var raw fileShape
	if err := safeyaml.DecodeStrict(data, &raw); err != nil {
		// yaml errors can quote the offending line, which may contain a
		// secret, so only a generic message is returned.
		return nil, errors.New("secrets file: invalid YAML or unknown key (details withheld to avoid echoing secret values)")
	}
	key, err := base64.StdEncoding.DecodeString(raw.SessionEncryptionKey)
	if raw.SessionEncryptionKey == "" {
		return nil, errors.New("secrets file: sessionEncryptionKey is required (32 random bytes, base64)")
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("secrets file: sessionEncryptionKey must be exactly 32 bytes, base64-encoded")
	}
	s := &Secrets{SessionEncryptionKey: New(key), Integrations: map[string]Secret{}}
	for i := range key {
		key[i] = 0
	}
	if raw.SigningKey != "" {
		seed, err := base64.StdEncoding.DecodeString(raw.SigningKey)
		if err != nil || len(seed) != 32 {
			return nil, errors.New("secrets file: signingKey must be a 32-byte Ed25519 seed, base64-encoded")
		}
		s.SigningKey = New(seed)
	}
	if raw.JellyfinAdminKey != "" {
		s.JellyfinAdminKey = New([]byte(raw.JellyfinAdminKey))
	}
	for name, v := range raw.Integrations {
		if v == "" {
			return nil, fmt.Errorf("secrets file: integrations.%s is empty", name)
		}
		s.Integrations[name] = New([]byte(v))
	}
	return s, nil
}
