// Package hostmetrics reads read-only system metrics from /proc and /sys
// (A-04, R-13). Inside a container these report host-wide values; no extra
// mounts or privileges are used.
package hostmetrics

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Reader reads from configurable roots (tests use fixture trees).
type Reader struct {
	Proc string // default /proc
	Sys  string // default /sys

	prevIdle, prevTotal uint64
}

// New returns a reader for the real system.
func New() *Reader { return &Reader{Proc: "/proc", Sys: "/sys"} }

// System is the system health widget data.
type System struct {
	CPUPercent float64    `json:"cpuPercent"`
	MemUsed    uint64     `json:"memUsed"`
	MemTotal   uint64     `json:"memTotal"`
	Load       [3]float64 `json:"load"`
	TempC      *float64   `json:"tempC"`
	UptimeSec  uint64     `json:"uptimeSec"`
}

// Read samples all system metrics. CPU percent is computed between calls
// (the first call reports 0).
func (r *Reader) Read() (System, error) {
	var s System
	idle, total, err := r.cpu()
	if err != nil {
		return s, err
	}
	if r.prevTotal > 0 && total > r.prevTotal {
		dt, di := total-r.prevTotal, idle-r.prevIdle
		s.CPUPercent = float64(dt-di) / float64(dt) * 100
	}
	r.prevIdle, r.prevTotal = idle, total
	if s.MemTotal, s.MemUsed, err = r.mem(); err != nil {
		return s, err
	}
	if s.Load, err = r.load(); err != nil {
		return s, err
	}
	if b, err := os.ReadFile(filepath.Join(r.Proc, "uptime")); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			v, _ := strconv.ParseFloat(f[0], 64)
			s.UptimeSec = uint64(v)
		}
	}
	s.TempC = r.temp()
	return s, nil
}

func (r *Reader) cpu() (idle, total uint64, err error) {
	f, err := os.Open(filepath.Join(r.Proc, "stat"))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		for i, v := range fields[1:] {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return 0, 0, errors.New("malformed /proc/stat")
			}
			total += n
			if i == 3 || i == 4 { // idle + iowait
				idle += n
			}
		}
		return idle, total, nil
	}
	return 0, 0, errors.New("no cpu line in /proc/stat")
}

func (r *Reader) mem() (total, used uint64, err error) {
	b, err := os.ReadFile(filepath.Join(r.Proc, "meminfo"))
	if err != nil {
		return 0, 0, err
	}
	vals := map[string]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			n, _ := strconv.ParseUint(f[1], 10, 64)
			vals[strings.TrimSuffix(f[0], ":")] = n * 1024
		}
	}
	total, avail := vals["MemTotal"], vals["MemAvailable"]
	if total == 0 {
		return 0, 0, errors.New("malformed /proc/meminfo")
	}
	if avail > total {
		avail = total
	}
	return total, total - avail, nil
}

func (r *Reader) load() ([3]float64, error) {
	var l [3]float64
	b, err := os.ReadFile(filepath.Join(r.Proc, "loadavg"))
	if err != nil {
		return l, err
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return l, errors.New("malformed /proc/loadavg")
	}
	for i := 0; i < 3; i++ {
		l[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return l, nil
}

// temp returns the highest sensor temperature, or nil when none is readable.
func (r *Reader) temp() *float64 {
	var best float64
	found := false
	globs := []string{
		filepath.Join(r.Sys, "class", "hwmon", "hwmon*", "temp*_input"),
		filepath.Join(r.Sys, "class", "thermal", "thermal_zone*", "temp"),
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			v, err := strconv.ParseFloat(string(bytes.TrimSpace(b)), 64)
			if err != nil || v <= 0 || v > 150000 {
				continue
			}
			if c := v / 1000; !found || c > best {
				best, found = c, true
			}
		}
		if found {
			break
		}
	}
	if !found {
		return nil
	}
	return &best
}

// Mount describes a mount point from mountinfo plus filesystem usage.
type Mount struct {
	Mounted  bool
	ReadOnly bool
	Free     *uint64
	Total    *uint64
}

// Mountinfo parses /proc/self/mountinfo into mount point -> read-only.
func (r *Reader) Mountinfo() (map[string]bool, error) {
	b, err := os.ReadFile(filepath.Join(r.Proc, "self", "mountinfo"))
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		point := unescape(f[4])
		ro := false
		for _, o := range strings.Split(f[5], ",") {
			if o == "ro" {
				ro = true
			}
		}
		out[point] = ro
	}
	return out, nil
}

func unescape(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

// Inspect reports the state of a mount point. It only calls statfs and never
// opens files under the mount (R-13).
func (r *Reader) Inspect(path string, info map[string]bool) Mount {
	ro, ok := info[path]
	if !ok {
		return Mount{}
	}
	m := Mount{Mounted: true, ReadOnly: ro}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err == nil {
		free := st.Bavail * uint64(st.Bsize)
		total := st.Blocks * uint64(st.Bsize)
		m.Free, m.Total = &free, &total
	}
	return m
}
