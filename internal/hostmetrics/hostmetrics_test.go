package hostmetrics

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadFixtures(t *testing.T) {
	root := t.TempDir()
	proc, sys := filepath.Join(root, "proc"), filepath.Join(root, "sys")
	write(t, proc, "stat", "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 1 1 1 1\n")
	write(t, proc, "meminfo", "MemTotal:       8000000 kB\nMemFree:  100 kB\nMemAvailable:   6000000 kB\n")
	write(t, proc, "loadavg", "0.50 0.25 0.10 1/200 1234\n")
	write(t, proc, "uptime", "3600.55 100.00\n")
	write(t, sys, "class/hwmon/hwmon0/temp1_input", "42000\n")
	write(t, sys, "class/hwmon/hwmon1/temp1_input", "55500\n")
	write(t, proc, "self/mountinfo", "22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw\n"+
		"40 22 8:2 / /mnt/media ro,relatime - fuseblk /dev/sdd2 ro\n"+
		"41 22 8:3 / /mnt/my\\040photos rw - exfat /dev/sda1 rw\n")

	r := &Reader{Proc: proc, Sys: sys}
	s, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPercent != 0 || s.MemTotal != 8000000*1024 || s.MemUsed != 2000000*1024 || s.Load[0] != 0.5 || s.UptimeSec != 3600 {
		t.Fatalf("first read: %+v", s)
	}
	if s.TempC == nil || *s.TempC != 55.5 {
		t.Fatalf("temp: %v", s.TempC)
	}
	// Second sample: 100 more busy, 100 more idle -> 50%.
	write(t, proc, "stat", "cpu  200 0 100 800 100 0 0 0 0 0\n")
	s, _ = r.Read()
	if s.CPUPercent != 50 {
		t.Fatalf("cpu: %v", s.CPUPercent)
	}
	info, err := r.Mountinfo()
	if err != nil {
		t.Fatal(err)
	}
	if ro, ok := info["/mnt/media"]; !ok || !ro {
		t.Fatal("media mount")
	}
	if ro, ok := info["/mnt/my photos"]; !ok || ro {
		t.Fatal("escaped mount")
	}
	if m := r.Inspect("/mnt/absent", info); m.Mounted {
		t.Fatal("absent mount reported mounted")
	}
	if m := r.Inspect("/", info); !m.Mounted || m.Total == nil {
		t.Fatalf("root mount: %+v", m)
	}
}

func TestMissingSensors(t *testing.T) {
	r := &Reader{Proc: t.TempDir(), Sys: t.TempDir()}
	if _, err := r.Read(); err == nil {
		t.Fatal("missing /proc should error")
	}
	if r.temp() != nil {
		t.Fatal("temp without sensors")
	}
}
