package server

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/importer"
)

func (s *Server) importRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/import", h(s.importFile))
	mux.HandleFunc("GET /api/icons/{name}", h(s.iconFile))
}

// importFile takes {"dryRun": bool, "file": <import file>}.
func (s *Server) importFile(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		DryRun bool            `json:"dryRun"`
		File   json.RawMessage `json:"file"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	f, err := importer.Parse(in.File)
	if err != nil {
		return err
	}
	rep, err := importer.Run(s.Store, f, in.DryRun, time.Now())
	if err != nil {
		return err
	}
	if !in.DryRun && (len(rep.Created) > 0 || len(rep.TagsCreated) > 0) {
		s.publish("item.changed", map[string]any{"change": "imported"})
	}
	writeJSON(w, http.StatusOK, rep)
	return nil
}

// iconFile serves an image from <home>/icons. Only names a file: icon
// reference may use are accepted, so nothing outside the folder is reachable.
func (s *Server) iconFile(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if !domain.ValidIconRef("file:"+name, false) {
		return fmt.Errorf("%w: bad icon name", domain.ErrInvalid)
	}
	b, err := os.ReadFile(filepath.Join(s.Home, importer.IconDir, name))
	if err != nil {
		return fmt.Errorf("%w: icon %s", domain.ErrNotFound, name)
	}
	ext := strings.ToLower(filepath.Ext(name))
	ct := mime.TypeByExtension(ext)
	switch ext {
	case ".svg":
		ct = "image/svg+xml"
		// an SVG is shown as an image only; never let it run anything
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	case ".ico":
		ct = "image/x-icon"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(b) //nolint:gosec // an image with its own type, nosniff, and a sandbox policy for SVG: never run as a page
	return nil
}
