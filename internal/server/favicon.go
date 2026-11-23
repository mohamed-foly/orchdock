package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/importer"
)

// A running service's own web icon becomes its icon. It is read only from
// the local machine, within size limits, after the service first answers,
// and refreshed at most once a day.
const (
	faviconPageMax  = 256 << 10
	faviconMax      = 512 << 10
	faviconTimeout  = 2 * time.Second
	faviconMaxAge   = 24 * time.Hour
	faviconFilePref = "favicon-"
)

var (
	linkTagRe = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	relRe     = regexp.MustCompile(`(?is)\brel\s*=\s*["']?([^"'>]+)`)
	hrefRe    = regexp.MustCompile(`(?is)\bhref\s*=\s*["']?([^"'\s>]+)`)
)

// faviconFile is the cached icon of an item ("" when there is none).
func (s *Server) faviconFile(itemID string) string {
	matches, _ := filepath.Glob(filepath.Join(s.Home, importer.IconDir, faviconFilePref+itemID+".*"))
	if len(matches) == 0 {
		return ""
	}
	return filepath.Base(matches[0])
}

// maybeFetchFavicon starts a fetch when the service has no icon yet or the
// cached one is a day old. One fetch per item at a time.
func (s *Server) maybeFetchFavicon(it domain.Item) {
	if f := s.faviconFile(it.ID); f != "" {
		if st, err := os.Stat(filepath.Join(s.Home, importer.IconDir, f)); err == nil && time.Since(st.ModTime()) < faviconMaxAge {
			return
		}
	}
	s.svc.mu.Lock()
	if s.svc.fetching == nil {
		s.svc.fetching = map[string]bool{}
	}
	if s.svc.fetching[it.ID] {
		s.svc.mu.Unlock()
		return
	}
	s.svc.fetching[it.ID] = true
	s.svc.mu.Unlock()
	go func() {
		defer func() {
			s.svc.mu.Lock()
			delete(s.svc.fetching, it.ID)
			s.svc.mu.Unlock()
		}()
		if err := s.fetchFavicon(it); err == nil {
			s.publish("item.changed", map[string]any{"itemId": it.ID, "change": "icon"})
		}
	}()
}

// fetchFavicon reads the service's page, finds its icon and stores it.
func (s *Server) fetchFavicon(it domain.Item) error {
	page := "http://127.0.0.1:" + strconv.Itoa(it.Service.Port) + "/"
	if u := strings.ReplaceAll(it.Service.URL, "{port}", strconv.Itoa(it.Service.Port)); u != "" {
		page = u
	}
	base, err := url.Parse(page)
	if err != nil || !local(base) {
		return errors.New("only local service pages are read")
	}
	candidates := []string{}
	if html, _, err := fetchLimited(base.String(), faviconPageMax); err == nil {
		candidates = iconLinks(html)
	}
	candidates = append(candidates, "/favicon.ico")
	for _, c := range candidates {
		ref, err := url.Parse(c)
		if err != nil {
			continue
		}
		u := base.ResolveReference(ref)
		if !local(u) {
			continue // never fetch from elsewhere
		}
		b, ctype, err := fetchLimited(u.String(), faviconMax)
		if err != nil || len(b) == 0 {
			continue
		}
		ext := imageExt(b, ctype)
		if ext == "" || (ext == ".svg" && !importer.SafeSVG(b)) {
			continue
		}
		return s.storeFavicon(it.ID, ext, b)
	}
	return errors.New("no icon found")
}

func (s *Server) storeFavicon(itemID, ext string, b []byte) error {
	dir := filepath.Join(s.Home, importer.IconDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(dir, faviconFilePref+itemID+".*"))
	for _, o := range old {
		_ = os.Remove(o)
	}
	return os.WriteFile(filepath.Join(dir, faviconFilePref+itemID+ext), b, 0o600)
}

// iconLinks lists the icon links of a page, best first.
func iconLinks(html []byte) []string {
	var icon, shortcut, apple []string
	for _, tag := range linkTagRe.FindAll(html, -1) {
		rel, href := relRe.FindSubmatch(tag), hrefRe.FindSubmatch(tag)
		if rel == nil || href == nil {
			continue
		}
		r := strings.ToLower(strings.TrimSpace(string(rel[1])))
		h := string(href[1])
		switch {
		case r == "icon":
			icon = append(icon, h)
		case strings.Contains(r, "shortcut") && strings.Contains(r, "icon"):
			shortcut = append(shortcut, h)
		case strings.Contains(r, "apple-touch-icon"):
			apple = append(apple, h)
		}
	}
	return append(append(icon, shortcut...), apple...)
}

func local(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	return (u.Scheme == "http" || u.Scheme == "https") && (h == "127.0.0.1" || h == "localhost" || h == "::1")
}

// fetchLimited GETs a local address, refusing bodies over max.
func fetchLimited(u string, max int64) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), faviconTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if !local(r.URL) {
			return errors.New("redirect away from this machine")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > max {
		return nil, "", errors.New("too large")
	}
	return b, resp.Header.Get("Content-Type"), nil
}

// imageExt names an image by its content: .png, .ico, .jpg or .svg ("" if none).
func imageExt(b []byte, ctype string) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return ".png"
	case bytes.HasPrefix(b, []byte{0, 0, 1, 0}):
		return ".ico"
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return ".jpg"
	case strings.Contains(strings.ToLower(ctype), "svg") || bytes.Contains(bytes.ToLower(b[:min(len(b), 512)]), []byte("<svg")):
		return ".svg"
	}
	return ""
}
