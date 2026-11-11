package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/whooslizi/kinkoku/store"
)

const defaultUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"

// transport sets a browser User-Agent and, when a FlareSolverr host is configured, solves
// Cloudflare challenges (403/503) once per request and retries with the clearance cookies.
type transport struct {
	base   http.RoundTripper
	jar    *store.Jar
	flare  string // FlareSolverr base URL, e.g. http://192.168.1.2:8191
	uaPath string

	mu  sync.Mutex
	uas map[string]string // host -> UA the clearance cookie is bound to
}

func newClient(data string, jar *store.Jar) *http.Client {
	t := &transport{
		base:   http.DefaultTransport,
		jar:    jar,
		flare:  strings.TrimSuffix(os.Getenv("KINKOKU_FLARESOLVERR"), "/"),
		uaPath: filepath.Join(data, "flaresolverr.json"),
		uas:    map[string]string{},
	}
	store.ReadJSON(t.uaPath, &t.uas)
	return &http.Client{Transport: t, Jar: jar, Timeout: 90 * time.Second}
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	ua := t.uas[req.URL.Hostname()]
	t.mu.Unlock()
	if ua != "" { // a clearance cookie only works with the UA that earned it
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", ua)
	} else if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", defaultUA)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil || t.flare == "" || !isChallenge(resp) || (req.Body != nil && req.GetBody == nil) {
		return resp, err
	}
	resp.Body.Close()
	if err := t.solve(req.URL.String(), req.URL.Hostname()); err != nil {
		return nil, fmt.Errorf("flaresolverr: %w", err)
	}
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		retry.Body, _ = req.GetBody()
	}
	t.mu.Lock()
	retry.Header.Set("User-Agent", t.uas[req.URL.Hostname()])
	t.mu.Unlock()
	retry.Header.Del("Cookie")
	for _, c := range t.jar.Cookies(req.URL) {
		retry.AddCookie(c)
	}
	return t.base.RoundTrip(retry)
}

func isChallenge(resp *http.Response) bool {
	if resp.StatusCode != 403 && resp.StatusCode != 503 {
		return false
	}
	return resp.Header.Get("Cf-Mitigated") == "challenge" ||
		strings.Contains(strings.ToLower(resp.Header.Get("Server")), "cloudflare") ||
		strings.Contains(strings.ToLower(resp.Header.Get("Server")), "ddos-guard")
}

func (t *transport) solve(u, host string) error {
	body, _ := json.Marshal(map[string]any{"cmd": "request.get", "url": u, "maxTimeout": 60000})
	c := &http.Client{Timeout: 90 * time.Second}
	resp, err := c.Post(t.flare+"/v1", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		Status   string `json:"status"`
		Message  string `json:"message"`
		Solution struct {
			UserAgent string `json:"userAgent"`
			Cookies   []struct {
				Name    string  `json:"name"`
				Value   string  `json:"value"`
				Domain  string  `json:"domain"`
				Path    string  `json:"path"`
				Expires float64 `json:"expires"`
				Secure  bool    `json:"secure"`
			} `json:"cookies"`
		} `json:"solution"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	if out.Status != "ok" {
		return fmt.Errorf("%s", out.Message)
	}
	for _, c := range out.Solution.Cookies {
		exp := int64(c.Expires)
		if exp < 0 {
			exp = 0
		}
		t.jar.Add(store.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Expires: exp, Secure: c.Secure})
	}
	if err := t.jar.Save(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.uas[host] = out.Solution.UserAgent
	return store.WriteJSON(t.uaPath, t.uas)
}
