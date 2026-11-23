package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
)

// Tags are the one way items are organised. A tag needs no
// definition to be used; a definition adds a folder (items working inside it
// are offered the tag), a colour and an icon.

func (s *Server) tagRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/tags/{tag}", h(s.tagPut))
	mux.HandleFunc("DELETE /api/tags/{tag}", h(s.tagDelete))
	mux.HandleFunc("GET /api/tags/suggest", h(s.tagSuggest))
}

// tagBody is what may be set on a tag; missing fields stay as they are.
type tagBody struct {
	Name  *string      `json:"name"` // a new name: renames the tag everywhere
	Path  *string      `json:"path"`
	Color *string      `json:"color"`
	Icon  *domain.Icon `json:"icon"`
}

// tagPut creates or changes a tag's definition.
func (s *Server) tagPut(w http.ResponseWriter, r *http.Request) error {
	var in tagBody
	if err := decode(r, &in); err != nil {
		return err
	}
	name := strings.ToLower(r.PathValue("tag"))
	if !domain.ValidTag(name) {
		return fmt.Errorf("%w: %q is not a valid tag", domain.ErrInvalid, name)
	}
	var saved domain.TagDef
	err := s.Store.Update(func(f *catalog.File) error {
		if in.Name != nil && strings.TrimSpace(*in.Name) != name {
			to := strings.ToLower(strings.TrimSpace(*in.Name))
			if err := f.RenameTag(name, to); err != nil {
				return err
			}
			name = to
		}
		d, _ := f.TagDefOf(name)
		d.Name = name
		if in.Path != nil {
			d.Path = strings.TrimSpace(*in.Path)
		}
		if in.Color != nil {
			d.Color = *in.Color
		}
		if in.Icon != nil {
			d.Icon = *in.Icon
		}
		if d.Path == "" && d.Color == "" && d.Icon.Primary == "" {
			f.RemoveTagDef(name, false) // nothing left to say about it
		} else {
			f.SetTagDef(d)
		}
		saved = d
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "tags"})
	writeJSON(w, http.StatusOK, saved)
	return nil
}

// tagDelete removes a tag's definition; ?fromItems=1 also takes the tag off
// every item.
func (s *Server) tagDelete(w http.ResponseWriter, r *http.Request) error {
	name := strings.ToLower(r.PathValue("tag"))
	fromItems := r.URL.Query().Get("fromItems") == "1"
	err := s.Store.Update(func(f *catalog.File) error {
		f.RemoveTagDef(name, fromItems)
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "tags"})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// tagSuggest answers ?cwd=<folder> with the tags whose folder holds it,
// deepest first. The window offers them; nothing is added by itself.
func (s *Server) tagSuggest(w http.ResponseWriter, r *http.Request) error {
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	tags := cat.SuggestTags(r.URL.Query().Get("cwd"))
	if tags == nil {
		tags = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
	return nil
}
