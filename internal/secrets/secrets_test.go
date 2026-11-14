package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sentinel = "S3NT1NEL-value-that-must-never-appear"

func TestSecretNeverFormats(t *testing.T) {
	s := New([]byte(sentinel))
	wrapper := struct {
		Name string
		Key  Secret
		Ptr  *Secret
	}{"x", s, &s}

	var outs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T"} {
		outs = append(outs, fmt.Sprintf(verb, s), fmt.Sprintf(verb, wrapper), fmt.Sprintf(verb, &s))
	}
	outs = append(outs, fmt.Sprint(s), fmt.Sprintln(s), s.String(), s.GoString())
	outs = append(outs, fmt.Errorf("wrap: %w", errors.New(fmt.Sprint(s))).Error())

	j, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	outs = append(outs, string(j))
	txt, _ := s.MarshalText()
	outs = append(outs, string(txt))

	var buf bytes.Buffer
	for _, h := range []slog.Handler{slog.NewTextHandler(&buf, nil), slog.NewJSONHandler(&buf, nil)} {
		l := slog.New(h)
		l.Info("msg", "key", s, "group", wrapper, slog.Any("any", s), slog.Any("ptr", &s))
	}
	outs = append(outs, buf.String())

	encoded := base64.StdEncoding.EncodeToString([]byte(sentinel))
	hexed := fmt.Sprintf("%x", []byte(sentinel))
	for _, o := range outs {
		if strings.Contains(o, sentinel) || strings.Contains(o, encoded) || strings.Contains(strings.ToLower(o), hexed) {
			t.Fatalf("secret leaked in %q", o)
		}
	}
	if got := string(s.Expose()); got != sentinel {
		t.Fatalf("Expose = %q", got)
	}
}

func writeSecrets(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secrets.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func validBody() string {
	return "sessionEncryptionKey: " + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)) + "\n"
}

func TestLoadPermissions(t *testing.T) {
	for _, m := range []os.FileMode{0o600, 0o400} {
		if _, err := Load(writeSecrets(t, validBody(), m)); err != nil {
			t.Errorf("mode %04o rejected: %v", m, err)
		}
	}
	for _, m := range []os.FileMode{0o640, 0o644, 0o604, 0o660, 0o610, 0o601} {
		if _, err := Load(writeSecrets(t, validBody(), m)); err == nil {
			t.Errorf("mode %04o accepted", m)
		}
	}
}

func TestLoadRejectsSymlinkAndDirectory(t *testing.T) {
	target := writeSecrets(t, validBody(), 0o600)
	link := filepath.Join(t.TempDir(), "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Error("symlink accepted")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("directory accepted")
	}
}

func TestCheckModeWrongOwner(t *testing.T) {
	p := writeSecrets(t, validBody(), 0o600)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkMode(p, st, os.Geteuid()); err != nil {
		t.Fatalf("own file rejected: %v", err)
	}
	if err := checkMode(p, st, os.Geteuid()+1); err == nil {
		t.Fatal("file owned by another uid accepted")
	}
}

func TestParseRejectsBadKeysWithoutEchoingValues(t *testing.T) {
	cases := map[string]string{
		"missing":   "",
		"short":     "sessionEncryptionKey: " + base64.StdEncoding.EncodeToString([]byte(sentinel[:16])) + "\n",
		"not b64":   "sessionEncryptionKey: " + sentinel + "\n",
		"unknown":   validBody() + "apiKey: " + sentinel + "\n",
		"bad yaml":  "sessionEncryptionKey: [" + sentinel + "\n",
		"duplicate": validBody() + validBody(),
	}
	for name, body := range cases {
		_, err := parse([]byte(body))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), sentinel) {
			t.Errorf("%s: error echoes secret: %v", name, err)
		}
	}
}

func TestLoadedKeyIsRedacted(t *testing.T) {
	s, err := Load(writeSecrets(t, validBody(), 0o600))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.SessionEncryptionKey.Expose()) != 32 {
		t.Fatal("key length")
	}
	if out := fmt.Sprintf("%+v %#v", s, *s); strings.Contains(out, "\x07") || !strings.Contains(out, Redacted) {
		t.Fatalf("formatted secrets struct = %q", out)
	}
}
