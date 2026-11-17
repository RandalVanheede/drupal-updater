package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer plays the dashboard: OAuth metadata, dynamic registration, authorize/token with PKCE, and an MCP endpoint.
type fakeServer struct {
	*httptest.Server
	mu          sync.Mutex
	challenge   string
	redirect    string
	expiresIn   int
	failRefresh bool
	sse         bool
	tokenCalls  []string
	mcpTokens   []string
	projects    []map[string]any
	stale       []string
}

func newFake(t *testing.T) *fakeServer {
	f := &fakeServer{expiresIn: 3600}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q]}`, f.URL+"/api/mcp", f.URL)
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"registration_endpoint":%q,"revocation_endpoint":%q,"code_challenge_methods_supported":["S256"]}`,
			f.URL, f.URL+"/oauth/authorize", f.URL+"/oauth/token", f.URL+"/oauth/register", f.URL+"/oauth/revoke")
	})
	mux.HandleFunc("/oauth/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
			AuthMethod   string   `json:"token_endpoint_auth_method"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.RedirectURIs) != 1 || !strings.HasPrefix(body.RedirectURIs[0], "http://127.0.0.1:") || body.AuthMethod != "none" {
			http.Error(w, "bad registration", 400)
			return
		}
		f.mu.Lock()
		f.redirect = body.RedirectURIs[0]
		f.mu.Unlock()
		w.WriteHeader(201)
		fmt.Fprint(w, `{"client_id":"client-1"}`)
	})
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "client-1" || q.Get("redirect_uri") != f.redirect {
			http.Error(w, "bad authorize request", 400)
			return
		}
		f.mu.Lock()
		f.challenge = q.Get("code_challenge")
		f.mu.Unlock()
		http.Redirect(w, r, f.redirect+"?code=the-code&state="+url.QueryEscape(q.Get("state"))+"&iss="+url.QueryEscape(f.URL), 302)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.tokenCalls = append(f.tokenCalls, r.Form.Get("grant_type"))
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant","error_description":"pkce"}`)
				return
			}
			fmt.Fprintf(w, `{"access_token":"at1","refresh_token":"rt1","expires_in":%d}`, f.expiresIn)
		case "refresh_token":
			if f.failRefresh || r.Form.Get("refresh_token") != "rt1" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"at2","expires_in":3600}`)
		}
	})
	mux.HandleFunc("/oauth/revoke", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/api/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.mcpTokens = append(f.mcpTokens, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer at1" && r.Header.Get("Authorization") != "Bearer at2" {
			w.WriteHeader(401)
			return
		}
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		reply := func(result string) {
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
			if f.sse {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
			} else {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			}
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			reply(`{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"fake","version":"1"}}`)
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/call":
			if r.Header.Get("Mcp-Session-Id") != "sess-1" {
				w.WriteHeader(400)
				return
			}
			args, _ := req.Params["arguments"].(map[string]any)
			list := f.projects
			if args["syncState"] == "outdated" {
				list = nil
				for _, p := range f.projects {
					for _, id := range f.stale {
						if p["id"] == id {
							list = append(list, p)
						}
					}
				}
			}
			// like the real server: securityUpdates is only returned (and only matches) when asked for
			var filtered []map[string]any
			for _, p := range list {
				_, has := p["securityUpdates"]
				if args["securityUpdates"] == true && !has {
					continue
				}
				if args["securityUpdates"] != true && has {
					q := map[string]any{}
					for k, v := range p {
						if k != "securityUpdates" {
							q[k] = v
						}
					}
					p = q
				}
				filtered = append(filtered, p)
			}
			list = filtered
			offset := int(args["offset"].(float64))
			page := []map[string]any{}
			for i := offset; i < len(list) && i < offset+2; i++ { // pages of two
				page = append(page, list[i])
			}
			text, _ := json.Marshal(map[string]any{"count": len(list), "returnedCount": len(page), "nextOffset": offset + len(page), "projects": page})
			content, _ := json.Marshal([]map[string]string{{"type": "text", "text": string(text)}, {"type": "text", "text": "Formatting rule"}})
			reply(`{"content":` + string(content) + `}`)
		}
	})
	return f
}

func newClient(t *testing.T, f *fakeServer) *Client {
	c, err := New(f.URL, FileStore{Path: filepath.Join(t.TempDir(), "dashboard.json")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// browser plays the user's browser: it opens the authorize URL, which redirects to the app's loopback callback.
func browser(u string) error {
	resp, err := http.Get(u)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func TestSignInWithPKCEAndCall(t *testing.T) {
	f := newFake(t)
	f.projects = []map[string]any{{"id": "1", "name": "ADVN"}}
	c := newClient(t, f)
	if c.SignedIn() {
		t.Fatal("fresh client should be signed out")
	}
	ctx := context.Background()
	if _, err := c.CallTool(ctx, "queryProjects", nil); err != ErrSignedOut {
		t.Fatalf("signed out call: %v", err)
	}
	if err := c.SignIn(ctx, browser); err != nil {
		t.Fatal(err)
	}
	if !c.SignedIn() {
		t.Fatal("should be signed in")
	}
	projects, err := c.DrupalProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0].Name != "ADVN" {
		t.Fatalf("projects %v %v", projects, err)
	}
	// the tokens survive a restart
	c2, _ := New(f.URL, c.store, nil)
	if !c2.SignedIn() {
		t.Fatal("stored tokens not found")
	}
	if err := c2.SignOut(ctx); err != nil || c2.SignedIn() {
		t.Fatalf("sign out: %v", err)
	}
}

func TestSignInIgnoresForeignCallbacks(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	probe := func(u string) error { // something else hits the loopback port first, with a forged state
		if resp, err := http.Get(f.redirect + "?code=evil&state=forged"); err != nil {
			return err
		} else {
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("a forged callback should be answered with 400, got %d", resp.StatusCode)
			}
		}
		return browser(u) // the real sign-in still completes
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.SignIn(ctx, probe); err != nil {
		t.Fatalf("a forged callback must not abort the sign-in: %v", err)
	}
	if !c.SignedIn() {
		t.Fatal("the genuine answer should sign in")
	}
}

func TestTokenRequestsDoNotFollowRedirectsToOtherHosts(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the token request body reached another host: %s", r.URL)
	}))
	defer other.Close()
	f := newFake(t)
	f.Config.Handler.(*http.ServeMux).HandleFunc("/oauth/token-redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	})
	c := newClient(t, f)
	if _, err := postToken(context.Background(), c.hc, f.URL+"/oauth/token-redirect", url.Values{"refresh_token": {"rt1"}}); err == nil {
		t.Fatal("a cross-host redirect should fail the request")
	}
}

func TestRefreshesAnExpiredToken(t *testing.T) {
	f := newFake(t)
	f.expiresIn = 1 // expires inside the 30s safety margin straight away
	f.projects = []map[string]any{{"id": "1", "name": "ADVN"}}
	c := newClient(t, f)
	ctx := context.Background()
	if err := c.SignIn(ctx, browser); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DrupalProjects(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.mcpTokens[len(f.mcpTokens)-1]; got != "Bearer at2" {
		t.Fatalf("calls should use the refreshed token, used %q", got)
	}
}

func TestFailedRefreshSignsOut(t *testing.T) {
	f := newFake(t)
	f.expiresIn = 1
	f.failRefresh = true
	c := newClient(t, f)
	ctx := context.Background()
	if err := c.SignIn(ctx, browser); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallTool(ctx, "queryProjects", nil); err != ErrSignedOut {
		t.Fatalf("got %v", err)
	}
	if c.SignedIn() {
		t.Fatal("a dead grant should clear the stored tokens")
	}
}

func TestEventStreamAnswersAndPaging(t *testing.T) {
	f := newFake(t)
	f.sse = true
	f.projects = []map[string]any{{"id": "1", "name": "A"}, {"id": "2", "name": "B", "securityUpdates": []string{"drupal", "webform"}}, {"id": "3", "name": "C"}}
	f.stale = []string{"2"}
	c := newClient(t, f)
	ctx := context.Background()
	if err := c.SignIn(ctx, browser); err != nil {
		t.Fatal(err)
	}
	projects, err := c.DrupalProjects(ctx)
	if err != nil || len(projects) != 3 {
		t.Fatalf("paging over event streams: %v %v", projects, err)
	}
	for _, p := range projects {
		if p.Stale != (p.ID == "2") {
			t.Fatalf("stale flag wrong for %s", p.ID)
		}
		if !p.SecurityKnown || (p.ID == "2") != (len(p.SecurityUpdates) == 2) {
			t.Fatalf("pending security modules not merged for %s: %+v", p.ID, p)
		}
	}
}

func TestServerFrom(t *testing.T) {
	for _, bad := range []string{"http://applications.example.com", "applications.example.com", ""} {
		if _, err := ServerFrom(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	for in, want := range map[string]string{
		"https://applications.example.com/":        "https://applications.example.com",
		"https://applications.example.com/api/mcp": "https://applications.example.com", // the full MCP address works too
		" https://applications.example.com ":       "https://applications.example.com",
		"http://127.0.0.1:8080/api/mcp":            "http://127.0.0.1:8080",
	} {
		if got, err := ServerFrom(in); err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
}

func TestDiscoveryRefusesForeignEndpoints(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "oauth-protected-resource") {
			fmt.Fprintf(w, `{"resource":"http://%s/api/mcp","authorization_servers":["http://%s"]}`, r.Host, r.Host)
			return
		}
		fmt.Fprintf(w, `{"issuer":"http://%s","authorization_endpoint":"https://phish.example/authorize","token_endpoint":"https://phish.example/token","registration_endpoint":"https://phish.example/register","code_challenge_methods_supported":["S256"]}`, r.Host)
	}))
	defer evil.Close()
	c, _ := New(evil.URL, FileStore{Path: filepath.Join(t.TempDir(), "d.json")}, nil)
	if err := c.SignIn(context.Background(), func(string) error { t.Fatal("must not open the browser"); return nil }); err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("got %v", err)
	}
}

func TestRef(t *testing.T) {
	ps := []Project{{ID: "1", Name: "Agora Website"}, {ID: "5", Name: "Twin"}, {ID: "6", Name: "twin"}}
	if Ref(ps[0], ps) != "Agora Website" || Ref(ps[1], ps) != "5" {
		t.Fatalf("refs: %q %q", Ref(ps[0], ps), Ref(ps[1], ps))
	}
}
