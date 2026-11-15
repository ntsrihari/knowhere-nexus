// Package jellyfin authenticates users against Jellyfin (R-10). Tokens are
// used once to read identity and role, then logged out and discarded.
package jellyfin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ntsrihari/knowhere-nexus/internal/buildinfo"
	"github.com/ntsrihari/knowhere-nexus/internal/upstream"
)

// ErrInvalidCredentials is returned for any rejected login.
var ErrInvalidCredentials = errors.New("invalid credentials")

// Identity is what Nexus keeps about a Jellyfin user.
type Identity struct {
	UserID  string
	Name    string
	IsAdmin bool
}

// Client is a Jellyfin authentication client.
type Client struct{ up *upstream.Client }

// New returns a client for the configured Jellyfin base URL.
func New(up *upstream.Client) *Client { return &Client{up: up} }

// Upstream exposes the underlying client (status reporting).
func (c *Client) Upstream() *upstream.Client { return c.up }

func newDeviceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "nexus-" + hex.EncodeToString(b[:])
}

func quote(s string) string { return strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(s) }

// authHeader builds the MediaBrowser authorization header.
func authHeader(deviceID, token string) http.Header {
	v := fmt.Sprintf(`MediaBrowser Client="Knowhere Nexus", Device="Nexus", DeviceId="%s", Version="%s"`,
		quote(deviceID), quote(buildinfo.Version))
	if token != "" {
		v += fmt.Sprintf(`, Token="%s"`, quote(token))
	}
	return http.Header{"Authorization": {v}}
}

type authResult struct {
	User struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
			IsDisabled      bool `json:"IsDisabled"`
		} `json:"Policy"`
	} `json:"User"`
	AccessToken string `json:"AccessToken"`
}

func (r *authResult) identity() (Identity, error) {
	if r.User.ID == "" || r.AccessToken == "" {
		return Identity{}, errors.New("jellyfin: incomplete authentication response")
	}
	if r.User.Policy.IsDisabled {
		return Identity{}, ErrInvalidCredentials
	}
	return Identity{UserID: r.User.ID, Name: r.User.Name, IsAdmin: r.User.Policy.IsAdministrator}, nil
}

// Login authenticates with a username and password. The resulting Jellyfin
// session is logged out before returning (R-10).
func (c *Client) Login(ctx context.Context, username, password string) (Identity, error) {
	dev := newDeviceID()
	var res authResult
	err := c.up.Do(ctx, upstream.Request{
		Method: http.MethodPost,
		Path:   "/Users/AuthenticateByName",
		Header: authHeader(dev, ""),
		Body:   map[string]string{"Username": username, "Pw": password},
	}, &res)
	if upstream.IsStatus(err, 401) || upstream.IsStatus(err, 403) || upstream.IsStatus(err, 400) {
		return Identity{}, ErrInvalidCredentials
	}
	if err != nil {
		return Identity{}, err
	}
	return c.finish(ctx, dev, &res)
}

func (c *Client) finish(ctx context.Context, dev string, res *authResult) (Identity, error) {
	id, err := res.identity()
	// Always discard the temporary Jellyfin session, even on error.
	if res.AccessToken != "" {
		c.logout(ctx, dev, res.AccessToken)
	}
	return id, err
}

func (c *Client) logout(ctx context.Context, dev, token string) {
	_, _ = c.up.DoRaw(context.WithoutCancel(ctx), upstream.Request{
		Method: http.MethodPost, Path: "/Sessions/Logout", Header: authHeader(dev, token),
	})
}

// QuickConnect is a pending Quick Connect request. Secret never leaves Nexus.
type QuickConnect struct {
	Code     string
	Secret   string
	DeviceID string
}

// StartQuickConnect initiates Quick Connect.
func (c *Client) StartQuickConnect(ctx context.Context) (QuickConnect, error) {
	dev := newDeviceID()
	var res struct {
		Secret string `json:"Secret"`
		Code   string `json:"Code"`
	}
	err := c.up.Do(ctx, upstream.Request{Method: http.MethodPost, Path: "/QuickConnect/Initiate", Header: authHeader(dev, "")}, &res)
	if upstream.IsStatus(err, 401) || upstream.IsStatus(err, 403) {
		return QuickConnect{}, errors.New("quick connect is disabled on the Jellyfin server")
	}
	if err != nil {
		return QuickConnect{}, err
	}
	if res.Secret == "" || res.Code == "" {
		return QuickConnect{}, errors.New("jellyfin: incomplete quick connect response")
	}
	return QuickConnect{Code: res.Code, Secret: res.Secret, DeviceID: dev}, nil
}

// PollQuickConnect reports whether the request was approved; if so it
// completes authentication and returns the identity.
func (c *Client) PollQuickConnect(ctx context.Context, qc QuickConnect) (Identity, bool, error) {
	var st struct {
		Authenticated bool `json:"Authenticated"`
	}
	err := c.up.Do(ctx, upstream.Request{
		Path: "/QuickConnect/Connect", Query: map[string]string{"secret": qc.Secret}, Header: authHeader(qc.DeviceID, ""),
	}, &st)
	if upstream.IsStatus(err, 404) || upstream.IsStatus(err, 401) {
		return Identity{}, false, ErrInvalidCredentials
	}
	if err != nil {
		return Identity{}, false, err
	}
	if !st.Authenticated {
		return Identity{}, false, nil
	}
	var res authResult
	err = c.up.Do(ctx, upstream.Request{
		Method: http.MethodPost, Path: "/Users/AuthenticateWithQuickConnect",
		Header: authHeader(qc.DeviceID, ""), Body: map[string]string{"Secret": qc.Secret},
	}, &res)
	if err != nil {
		return Identity{}, false, err
	}
	id, err := c.finish(ctx, qc.DeviceID, &res)
	return id, err == nil, err
}

// Validate checks a Jellyfin access token presented by Milano and returns its
// identity (token exchange, F-25). The token is not stored or logged out:
// it belongs to the Milano client.
func (c *Client) Validate(ctx context.Context, token string) (Identity, error) {
	var res struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
			IsDisabled      bool `json:"IsDisabled"`
		} `json:"Policy"`
	}
	err := c.up.Do(ctx, upstream.Request{Path: "/Users/Me", Header: authHeader(newDeviceID(), token)}, &res)
	if upstream.IsStatus(err, 401) || upstream.IsStatus(err, 403) {
		return Identity{}, ErrInvalidCredentials
	}
	if err != nil {
		return Identity{}, err
	}
	if res.ID == "" || res.Policy.IsDisabled {
		return Identity{}, ErrInvalidCredentials
	}
	return Identity{UserID: res.ID, Name: res.Name, IsAdmin: res.Policy.IsAdministrator}, nil
}
