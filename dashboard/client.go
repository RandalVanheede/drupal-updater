package dashboard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"drupal-updater/config"
)

// ErrSignedOut means there are no usable tokens: the user has to sign in again.
var ErrSignedOut = errors.New("not signed in to the dashboard")

// Store keeps the tokens between runs.
type Store interface {
	Load() (*Tokens, error) // (nil, nil) when nothing is stored
	Save(*Tokens) error
	Clear() error
}

// FileStore keeps the tokens in one file readable by the user only.
type FileStore struct{ Path string }

func (s FileStore) Load() (*Tokens, error) {
	data, err := os.ReadFile(s.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var t Tokens
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, nil // unreadable = signed out; signing in again rewrites it
	}
	return &t, nil
}

func (s FileStore) Save(t *Tokens) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dirOf(s.Path), 0o700); err != nil {
		return err
	}
	return config.WriteFileAtomic(s.Path, data, 0o600)
}

func (s FileStore) Clear() error {
	if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "."
}

// Client talks to one dashboard server. Create it with New; it is safe for concurrent use.
type Client struct {
	server string
	hc     *http.Client
	store  Store

	mu      sync.Mutex // guards tokens, meta and session
	tokens  *Tokens
	meta    *metadata
	session string
	nextID  int
}

// ServerFrom turns what the user typed (the server root, or the full MCP address) into the server root:
// scheme and host only. It must be https, except for localhost (tests).
func ServerFrom(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a server address like https://applications.example.com", raw)
	}
	host := u.Hostname()
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "127.0.0.1" || host == "localhost")) {
		return "", errors.New("the dashboard address must start with https://")
	}
	return u.Scheme + "://" + u.Host, nil
}

// New returns a client for a dashboard server (see ServerFrom for the accepted addresses).
func New(server string, store Store, hc *http.Client) (*Client, error) {
	server, err := ServerFrom(server)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if r.URL.Host != via[0].URL.Host { // a redirect would replay a token request body (code, refresh token) elsewhere
				return http.ErrUseLastResponse
			}
			return nil
		}}
	}
	return &Client{server: server, hc: hc, store: store}, nil
}

func (c *Client) discoverLocked(ctx context.Context) (*metadata, error) {
	if c.meta == nil {
		m, err := discover(ctx, c.hc, c.server)
		if err != nil {
			return nil, err
		}
		c.meta = m
	}
	return c.meta, nil
}

// SignedIn reports whether tokens are stored (not whether they are still valid).
func (c *Client) SignedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokens == nil {
		t, _ := c.store.Load()
		c.tokens = t
	}
	return c.tokens != nil && c.tokens.Server == c.server
}

// SignIn runs the browser sign-in; open receives the URL to show to the user. It does not hold the client's
// lock while the user is in the browser, so SignedIn and other calls stay responsive.
func (c *Client) SignIn(ctx context.Context, open func(string) error) error {
	c.mu.Lock()
	m, err := c.discoverLocked(ctx)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	t, err := signIn(ctx, c.hc, c.server, m, open)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.tokens, c.session = t, ""
	c.mu.Unlock()
	return c.store.Save(t)
}

// SignOut forgets the tokens and tells the server to revoke the refresh token (best effort).
func (c *Client) SignOut(ctx context.Context) error {
	c.mu.Lock()
	t, m := c.tokens, c.meta
	if t == nil {
		t, _ = c.store.Load()
	}
	c.tokens, c.session = nil, ""
	err := c.store.Clear() // under the lock: a concurrent call must not load the old tokens back
	c.mu.Unlock()
	if t != nil && t.RefreshToken != "" && m != nil && m.Revoke != "" {
		form := url.Values{"token": {t.RefreshToken}, "client_id": {t.ClientID}}
		if req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, m.Revoke, strings.NewReader(form.Encode())); rerr == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if resp, rerr := c.hc.Do(req); rerr == nil {
				resp.Body.Close()
			}
		}
	}
	return err
}

// accessToken returns a valid access token, refreshing it when it is about to expire.
func (c *Client) accessToken(ctx context.Context, forceRefresh bool) (string, error) {
	if c.tokens == nil {
		t, err := c.store.Load()
		if err != nil {
			return "", err
		}
		c.tokens = t
	}
	if c.tokens == nil || c.tokens.Server != c.server {
		return "", ErrSignedOut
	}
	if forceRefresh || (!c.tokens.Expiry.IsZero() && time.Until(c.tokens.Expiry) < 30*time.Second) {
		m, err := c.discoverLocked(ctx)
		if err != nil {
			return "", err
		}
		t, err := refresh(ctx, c.hc, m, c.tokens)
		if err != nil {
			c.tokens = nil
			_ = c.store.Clear() // a refresh that fails means the grant is gone: sign in again
			return "", ErrSignedOut
		}
		c.tokens = t
		if err := c.store.Save(t); err != nil {
			return "", err
		}
	}
	return c.tokens.AccessToken, nil
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// post sends one JSON-RPC message to the MCP endpoint and returns the response with the matching id (the
// server may answer with plain JSON or an event stream). notification = no answer expected.
func (c *Client) post(ctx context.Context, m *metadata, token string, body map[string]any, id int, notification bool) (*rpcResponse, int, error) {
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Resource, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
		req.Header.Set("Mcp-Protocol-Version", "2025-03-26")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.session = s
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, resp.StatusCode, fmt.Errorf("dashboard: %s", resp.Status)
	}
	if resp.StatusCode/100 != 2 {
		return nil, resp.StatusCode, fmt.Errorf("dashboard: %s", resp.Status)
	}
	if notification {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, resp.StatusCode, nil
	}
	want := fmt.Sprint(id)
	var found *rpcResponse
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(io.LimitReader(resp.Body, maxBody))
		sc.Buffer(make([]byte, 1<<20), maxBody)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var r rpcResponse
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &r) == nil && string(r.ID) == want {
				found = &r
				break
			}
		}
		if found == nil {
			return nil, resp.StatusCode, errors.New("dashboard: the event stream held no answer")
		}
	} else {
		var r rpcResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&r); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("dashboard: unreadable answer: %w", err)
		}
		found = &r
	}
	if found.Error != nil {
		return nil, resp.StatusCode, fmt.Errorf("dashboard: %s", found.Error.Message)
	}
	return found, resp.StatusCode, nil
}

func (c *Client) initLocked(ctx context.Context, m *metadata, token string) error {
	c.nextID++
	_, _, err := c.post(ctx, m, token, map[string]any{
		"jsonrpc": "2.0", "id": c.nextID, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "drupal-updater", "version": "1"}},
	}, c.nextID, false)
	if err != nil {
		return err
	}
	_, _, err = c.post(ctx, m, token, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, 0, true)
	return err
}

// CallTool calls one tool and returns the text of its first content block.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := c.discoverLocked(ctx)
	if err != nil {
		return "", err
	}
	refreshed := false
	for attempt := 0; attempt < 3; attempt++ {
		token, err := c.accessToken(ctx, false)
		if err != nil {
			return "", err
		}
		if c.session == "" {
			if err := c.initLocked(ctx, m, token); err != nil {
				if !refreshed && strings.Contains(err.Error(), "401") {
					refreshed = true
					if _, err := c.accessToken(ctx, true); err != nil {
						return "", err
					}
					continue
				}
				return "", err
			}
		}
		c.nextID++
		if args == nil {
			args = map[string]any{}
		}
		res, status, err := c.post(ctx, m, token, map[string]any{
			"jsonrpc": "2.0", "id": c.nextID, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args},
		}, c.nextID, false)
		switch {
		case err == nil:
			var out struct {
				IsError bool `json:"isError"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(res.Result, &out); err != nil || len(out.Content) == 0 {
				return "", errors.New("dashboard: the tool answered without content")
			}
			if out.IsError {
				return "", fmt.Errorf("dashboard: %s", out.Content[0].Text)
			}
			return out.Content[0].Text, nil
		case status == http.StatusUnauthorized && !refreshed:
			refreshed = true
			if _, err := c.accessToken(ctx, true); err != nil {
				return "", err
			}
		case (status == http.StatusNotFound || status == http.StatusBadRequest) && c.session != "":
			c.session = "" // the session expired: initialise again
		default:
			return "", err
		}
	}
	return "", errors.New("dashboard: could not complete the call")
}
