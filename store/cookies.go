package store

import (
	"bufio"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"` // without leading dot; matches subdomains too
	Path     string `json:"path"`
	Expires  int64  `json:"expires,omitempty"` // unix seconds, 0 = session
	Secure   bool   `json:"secure,omitempty"`
	HostOnly bool   `json:"hostOnly,omitempty"`
}

// Jar is a small persistent http.CookieJar backed by <data>/cookies.json.
type Jar struct {
	path    string
	mu      sync.Mutex
	cookies []Cookie
	dirty   bool
}

func LoadJar(dataDir string) (*Jar, error) {
	j := &Jar{path: filepath.Join(dataDir, "cookies.json")}
	return j, ReadJSON(j.path, &j.cookies)
}

func (j *Jar) Save() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.dirty {
		return nil
	}
	j.dirty = false
	now := time.Now().Unix()
	live := []Cookie{}
	for _, c := range j.cookies {
		if c.Expires == 0 || c.Expires > now {
			live = append(live, c)
		}
	}
	return WriteJSON(j.path, live)
}

func (j *Jar) Clear() error {
	j.mu.Lock()
	j.cookies, j.dirty = nil, true
	j.mu.Unlock()
	return j.Save()
}

func (j *Jar) Add(c Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	c.Domain = strings.TrimPrefix(strings.ToLower(c.Domain), ".")
	if c.Path == "" {
		c.Path = "/"
	}
	j.dirty = true
	for i, o := range j.cookies {
		if o.Name == c.Name && o.Domain == c.Domain && o.Path == c.Path {
			j.cookies[i] = c
			return
		}
	}
	j.cookies = append(j.cookies, c)
}

func (j *Jar) SetCookies(u *url.URL, cs []*http.Cookie) {
	for _, c := range cs {
		nc := Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Secure: c.Secure}
		if nc.Domain == "" {
			nc.Domain, nc.HostOnly = u.Hostname(), true
		}
		switch {
		case c.MaxAge < 0:
			nc.Expires = 1 // already expired; Save drops it
		case c.MaxAge > 0:
			nc.Expires = time.Now().Unix() + int64(c.MaxAge)
		case !c.Expires.IsZero():
			nc.Expires = c.Expires.Unix()
		}
		j.Add(nc)
	}
}

func (j *Jar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	host := strings.ToLower(u.Hostname())
	path := u.Path
	if path == "" {
		path = "/"
	}
	now := time.Now().Unix()
	var out []*http.Cookie
	for _, c := range j.cookies {
		domainOK := host == c.Domain || (!c.HostOnly && strings.HasSuffix(host, "."+c.Domain))
		if !domainOK || !strings.HasPrefix(path, c.Path) || (c.Secure && u.Scheme != "https") ||
			(c.Expires != 0 && c.Expires <= now) {
			continue
		}
		out = append(out, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	return out
}

// ImportNetscape adds every cookie from a Netscape cookies.txt and returns how many it read.
func (j *Jar) ImportNetscape(r io.Reader) (int, error) {
	n := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		exp, _ := strconv.ParseInt(f[4], 10, 64)
		j.Add(Cookie{
			Domain:   f[0],
			HostOnly: !strings.EqualFold(f[1], "TRUE"),
			Path:     f[2],
			Secure:   strings.EqualFold(f[3], "TRUE"),
			Expires:  exp,
			Name:     f[5],
			Value:    strings.Join(f[6:], "\t"),
		})
		n++
	}
	return n, sc.Err()
}
