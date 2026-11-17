// Package dashboard is a small read-only client for Dynamate's applications dashboard MCP server
// (streamable HTTP + OAuth 2.1 with PKCE): it signs in through the browser, keeps the tokens, and calls tools.
package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Tokens are what a sign-in leaves behind. ClientID belongs to the dynamically registered client that the
// refresh token was issued to.
type Tokens struct {
	Server       string    `json:"server"`
	ClientID     string    `json:"client_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry"`
}

// metadata is what discovery finds: the MCP endpoint and the OAuth endpoints that guard it.
type metadata struct {
	Resource     string
	Issuer       string
	Authorize    string
	Token        string
	Register     string
	Revoke       string
	SupportsS256 bool
}

const maxBody = 16 << 20

func getJSON(ctx context.Context, hc *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", u, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out)
}

// discover reads the OAuth protected-resource metadata of the server, then its authorization server's.
func discover(ctx context.Context, hc *http.Client, server string) (*metadata, error) {
	var pr struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := getJSON(ctx, hc, server+"/.well-known/oauth-protected-resource", &pr); err != nil {
		return nil, fmt.Errorf("discovering the dashboard: %w", err)
	}
	if pr.Resource == "" || len(pr.AuthorizationServers) == 0 {
		return nil, errors.New("the dashboard did not describe an authorization server")
	}
	var as struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RegistrationEndpoint  string   `json:"registration_endpoint"`
		RevocationEndpoint    string   `json:"revocation_endpoint"`
		CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	}
	issuer := strings.TrimRight(pr.AuthorizationServers[0], "/")
	if err := sameHost(server, issuer); err != nil { // before any request goes there
		return nil, err
	}
	if err := getJSON(ctx, hc, issuer+"/.well-known/oauth-authorization-server", &as); err != nil {
		return nil, fmt.Errorf("discovering the authorization server: %w", err)
	}
	if as.Issuer != issuer { // RFC 8414: the metadata must name the server it was fetched from
		return nil, errors.New("the authorization server metadata names a different issuer")
	}
	m := &metadata{Resource: pr.Resource, Issuer: as.Issuer, Authorize: as.AuthorizationEndpoint, Token: as.TokenEndpoint, Register: as.RegistrationEndpoint, Revoke: as.RevocationEndpoint}
	for _, c := range as.CodeChallengeMethods {
		m.SupportsS256 = m.SupportsS256 || c == "S256"
	}
	if m.Authorize == "" || m.Token == "" || m.Register == "" {
		return nil, errors.New("the authorization server lacks an authorize, token or registration endpoint")
	}
	if !m.SupportsS256 {
		return nil, errors.New("the authorization server does not offer PKCE (S256)")
	}
	// Everything the metadata points at must live on the server the user configured: a tampered answer must not
	// be able to send the browser, or the tokens, somewhere else.
	for _, e := range []string{m.Resource, m.Authorize, m.Token, m.Register, m.Revoke} {
		if e == "" {
			continue
		}
		if err := sameHost(server, e); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// sameHost fails unless u has the scheme and host of server.
func sameHost(server, u string) error {
	base, _ := url.Parse(server)
	p, err := url.Parse(u)
	if err != nil || base == nil || p.Scheme != base.Scheme || p.Host != base.Host {
		return fmt.Errorf("the dashboard points at another host (%s)", u)
	}
	return nil
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// register creates a public OAuth client for this sign-in (the loopback port changes every time).
func register(ctx context.Context, hc *http.Client, m *metadata, redirect string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                "Drupal Updater",
		"redirect_uris":              []string{redirect},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"scope":                      "read",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Register, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("registering the client: %s", resp.Status)
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil || out.ClientID == "" {
		return "", errors.New("the dashboard returned no client id")
	}
	return out.ClientID, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

func postToken(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var tr tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&tr); err != nil {
		return nil, fmt.Errorf("token endpoint: %s", resp.Status)
	}
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint: %s %s", tr.Error, tr.Description)
	}
	return &tr, nil
}

func tokensFrom(server, clientID string, tr *tokenResponse, prevRefresh string) *Tokens {
	t := &Tokens{Server: server, ClientID: clientID, AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken}
	if t.RefreshToken == "" {
		t.RefreshToken = prevRefresh // the server may not rotate it
	}
	if tr.ExpiresIn > 0 {
		t.Expiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return t
}

// signIn runs the authorization-code flow with PKCE: a loopback listener catches the redirect, open sends the
// user to the dashboard's login page. It returns once the code was exchanged, or when ctx ends.
func signIn(ctx context.Context, hc *http.Client, server string, m *metadata, open func(string) error) (*Tokens, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0") // loopback only: nothing else on the network can reach the callback
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	clientID, err := register(ctx, hc, m, redirect)
	if err != nil {
		return nil, err
	}
	verifier, err := randomString(48)
	if err != nil {
		return nil, err
	}
	state, err := randomString(24)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"scope":                 {"read"},
		"resource":              {m.Resource},
	}

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query()
		var res result
		if got.Get("state") != state {
			// not an answer to our request (another local process, a probing web page): ignore it, keep waiting
			http.Error(w, "unknown request", http.StatusBadRequest)
			return
		}
		switch {
		case got.Get("iss") != "" && m.Issuer != "" && got.Get("iss") != m.Issuer:
			res.err = errors.New("the sign-in answer came from an unexpected issuer")
		case got.Get("error") != "":
			res.err = fmt.Errorf("the dashboard refused the sign-in: %s", got.Get("error"))
		case got.Get("code") == "":
			res.err = errors.New("the sign-in answer had no code")
		default:
			res.code = got.Get("code")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<h3>Sign-in failed</h3><p>%s</p>", html.EscapeString(res.err.Error()))
		} else {
			fmt.Fprint(w, "<h3>Signed in</h3><p>You can close this tab and return to Drupal Updater.</p>")
		}
		select {
		case done <- res:
		default: // a second request for the same flow is ignored
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	if err := open(m.Authorize + "?" + q.Encode()); err != nil {
		return nil, fmt.Errorf("opening the browser: %w", err)
	}
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return nil, errors.New("sign-in cancelled or timed out")
	}
	if res.err != nil {
		return nil, res.err
	}
	tr, err := postToken(ctx, hc, m.Token, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {res.code},
		"redirect_uri":  {redirect},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	if err != nil {
		return nil, err
	}
	return tokensFrom(server, clientID, tr, ""), nil
}

func refresh(ctx context.Context, hc *http.Client, m *metadata, t *Tokens) (*Tokens, error) {
	if t.RefreshToken == "" {
		return nil, errors.New("signed out: no refresh token")
	}
	tr, err := postToken(ctx, hc, m.Token, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
		"client_id":     {t.ClientID},
	})
	if err != nil {
		return nil, err
	}
	return tokensFrom(t.Server, t.ClientID, tr, t.RefreshToken), nil
}
