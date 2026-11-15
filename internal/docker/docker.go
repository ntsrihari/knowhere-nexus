// Package docker is a read-only client for the Docker API behind the
// restricted socket proxy (A-03). It can issue exactly three GET requests;
// there is no code path for any write. Inspect output is reduced to an
// allowlist of fields: environment, mounts and labels never leave here.
package docker

import (
	"context"
	"regexp"
	"strings"

	"github.com/ntsrihari/knowhere-nexus/internal/upstream"
)

// Client reads container state.
type Client struct{ up *upstream.Client }

// New wraps an upstream client for the socket proxy.
func New(up *upstream.Client) *Client { return &Client{up: up} }

// Upstream exposes status.
func (c *Client) Upstream() *upstream.Client { return c.up }

// Container is the filtered view Nexus uses.
type Container struct {
	ID           string `json:"-"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        string `json:"state"`
	RestartCount int    `json:"restartCount"`
	MemBytes     uint64 `json:"memBytes"`
}

var idRe = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// allowedPaths documents the only requests this package can make (ST tests).
var allowedPaths = []string{"/containers/json", "/containers/{id}/json", "/containers/{id}/stats"}

// List returns all containers with restart counts and memory usage.
func (c *Client) List(ctx context.Context, withStats bool) ([]Container, error) {
	var raw []struct {
		ID    string   `json:"Id"`
		Names []string `json:"Names"`
		Image string   `json:"Image"`
		State string   `json:"State"`
	}
	if err := c.up.Do(ctx, upstream.Request{Path: "/containers/json", Query: map[string]string{"all": "1"}}, &raw); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		if !idRe.MatchString(r.ID) {
			continue
		}
		name := r.ID[:12]
		if len(r.Names) > 0 {
			name = strings.TrimPrefix(r.Names[0], "/")
		}
		ct := Container{ID: r.ID, Name: name, Image: r.Image, State: r.State}
		var insp struct {
			RestartCount int `json:"RestartCount"`
		}
		if err := c.up.Do(ctx, upstream.Request{Path: "/containers/" + r.ID + "/json"}, &insp); err == nil {
			ct.RestartCount = insp.RestartCount
		}
		if withStats && r.State == "running" {
			var st struct {
				MemoryStats struct {
					Usage uint64            `json:"usage"`
					Stats map[string]uint64 `json:"stats"`
				} `json:"memory_stats"`
			}
			if err := c.up.Do(ctx, upstream.Request{Path: "/containers/" + r.ID + "/stats",
				Query: map[string]string{"stream": "false", "one-shot": "true"}}, &st); err == nil {
				used := st.MemoryStats.Usage
				if cache := st.MemoryStats.Stats["inactive_file"]; cache < used {
					used -= cache
				}
				ct.MemBytes = used
			}
		}
		out = append(out, ct)
	}
	return out, nil
}
