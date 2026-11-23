package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/importer"
)

func (s *Server) iconPickRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/icons", h(s.iconList))
	mux.HandleFunc("POST /api/icons", h(s.iconUpload))
}

// iconList lists the images in the icons folder that a user can pick (not the
// cached web and project icons, which follow their item or project).
func (s *Server) iconList(w http.ResponseWriter, _ *http.Request) error {
	entries, _ := os.ReadDir(filepath.Join(s.Home, importer.IconDir))
	names := []string{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || strings.HasPrefix(n, faviconFilePref) || strings.HasPrefix(n, autoFilePref) || !domain.ValidIconRef("file:"+n, false) {
			continue
		}
		names = append(names, n)
	}
	slices.Sort(names)
	writeJSON(w, http.StatusOK, map[string]any{"icons": names})
	return nil
}

var nameCleanRe = regexp.MustCompile(`[^0-9a-z_-]+`)

// iconUpload takes {"name": "logo.png", "data": "<base64>"} and stores the
// image under a safe, unique name, which it returns.
func (s *Server) iconUpload(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name string `json:"name"`
		Data string `json:"data"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	b, err := base64.StdEncoding.DecodeString(in.Data)
	if err != nil {
		return fmt.Errorf("%w: the image is not base64", domain.ErrInvalid)
	}
	if len(b) == 0 || len(b) > importer.MaxIconBytes {
		return fmt.Errorf("%w: an icon must be between 1 byte and %d KB", domain.ErrInvalid, importer.MaxIconBytes>>10)
	}
	ext := imageExt(b, "")
	if ext == "" {
		return fmt.Errorf("%w: only png, jpg, ico and svg images can be icons", domain.ErrInvalid)
	}
	if ext == ".svg" && !importer.SafeSVG(b) {
		return fmt.Errorf("%w: the SVG contains scripts or event handlers", domain.ErrInvalid)
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(in.Name)), filepath.Ext(in.Name))
	base = strings.Trim(nameCleanRe.ReplaceAllString(base, "-"), "-_")
	if base == "" {
		base = "icon"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	name := base + "-" + hex.EncodeToString(suffix) + ext
	dir := filepath.Join(s.Home, importer.IconDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, map[string]any{"name": name, "ref": "file:" + name})
	return nil
}

// --- automatic project icons ---------------------------------------------------

const autoFilePref = "auto-"

// autoCandidates are the files looked for in a project folder, best first.
var autoCandidates = []string{
	"favicon.svg", "favicon.ico", "public/favicon.svg", "public/favicon.ico", "static/favicon.ico",
	"assets/icon.png", "assets/logo.png", "logo.png", "icon.png", ".idea/icon.svg",
}

// autoIcon returns the icon file of a tag whose icon is "auto": the
// first image found in its folder, copied into the icons folder (and copied
// again when the original changes). "" when there is none.
func (s *Server) autoIcon(p domain.TagDef) string {
	if p.Icon.Primary != "auto" || p.Path == "" {
		return ""
	}
	dir := filepath.Join(s.Home, importer.IconDir)
	for _, c := range autoCandidates {
		src := filepath.Join(p.Path, filepath.FromSlash(c))
		st, err := os.Stat(src)
		if err != nil || st.IsDir() || st.Size() == 0 || st.Size() > importer.MaxIconBytes {
			continue
		}
		ext := strings.ToLower(filepath.Ext(c))
		name := autoFilePref + p.Name + ext
		dst := filepath.Join(dir, name)
		if cur, err := os.Stat(dst); err == nil && !cur.ModTime().Before(st.ModTime()) {
			return name // the copy is current
		}
		b, err := os.ReadFile(src)
		if err != nil || imageExt(b, "") == "" || (ext == ".svg" && !importer.SafeSVG(b)) {
			continue
		}
		old, _ := filepath.Glob(filepath.Join(dir, autoFilePref+p.Name+".*"))
		for _, o := range old {
			_ = os.Remove(o)
		}
		if os.MkdirAll(dir, 0o700) != nil || os.WriteFile(dst, b, 0o600) != nil {
			return ""
		}
		return name
	}
	return ""
}

// tagDTO is a tag definition with what the window shows about it.
type tagDTO struct {
	domain.TagDef
	AutoIcon string `json:"autoIcon,omitempty"` // the found image of an "auto" icon
}

func (s *Server) tagsWithIcons(ds []domain.TagDef) []tagDTO {
	out := make([]tagDTO, len(ds))
	for i, d := range ds {
		out[i] = tagDTO{TagDef: d, AutoIcon: s.autoIcon(d)}
	}
	return out
}
