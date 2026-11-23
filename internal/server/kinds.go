package server

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
)

func (s *Server) kindRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/kinds", h(s.kindCreate))
	mux.HandleFunc("PATCH /api/kinds/{ref}", h(s.kindPatch))
	mux.HandleFunc("DELETE /api/kinds/{ref}", h(s.kindDelete))
	mux.HandleFunc("POST /api/kinds/{ref}/move", h(s.kindMove))
}

// kindMove takes {"by": -1} (up) or {"by": 1} (down).
func (s *Server) kindMove(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		By int `json:"by"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	err := s.Store.Update(func(f *catalog.File) error {
		k, err := f.FindKind(r.PathValue("ref"))
		if err != nil {
			return err
		}
		return f.MoveKind(k.ID, in.By)
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "kinds"})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type kindBody struct {
	Name  *string      `json:"name"`
	Base  *domain.Kind `json:"base"`
	Color *string      `json:"color"`
}

func (s *Server) kindCreate(w http.ResponseWriter, r *http.Request) error {
	var in kindBody
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" || in.Base == nil {
		return fmt.Errorf("%w: name and base are required", domain.ErrInvalid)
	}
	var created domain.KindDef
	err := s.Store.Update(func(f *catalog.File) error {
		name := strings.TrimSpace(*in.Name)
		if f.KindNameTaken(name, "") {
			return fmt.Errorf("%w: a kind named %q exists", domain.ErrConflict, name)
		}
		color := ""
		if in.Color != nil {
			color = *in.Color
		}
		created = domain.NewKind(name, *in.Base, color)
		f.Kinds = append(f.Kinds, created)
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "kinds"})
	writeJSON(w, http.StatusCreated, created)
	return nil
}

// kindPatch renames or recolours a kind; a built-in kind gets an entry the
// first time. The base of a kind never changes (its items depend on it).
func (s *Server) kindPatch(w http.ResponseWriter, r *http.Request) error {
	var in kindBody
	if err := decode(r, &in); err != nil {
		return err
	}
	var updated domain.KindDef
	err := s.Store.Update(func(f *catalog.File) error {
		k, err := f.FindKind(r.PathValue("ref"))
		if err != nil {
			return err
		}
		if in.Base != nil && *in.Base != k.Base {
			return fmt.Errorf("%w: the base of a kind cannot change; make a new kind instead", domain.ErrInvalid)
		}
		if in.Name != nil {
			name := strings.TrimSpace(*in.Name)
			if f.KindNameTaken(name, k.ID) {
				return fmt.Errorf("%w: a kind named %q exists", domain.ErrConflict, name)
			}
			k.Name = name
		}
		if in.Color != nil {
			k.Color = *in.Color
		}
		if i := slices.IndexFunc(f.Kinds, func(x domain.KindDef) bool { return x.ID == k.ID }); i >= 0 {
			f.Kinds[i] = k
		} else {
			f.Kinds = append(f.Kinds, k)
		}
		updated = k
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "kinds"})
	writeJSON(w, http.StatusOK, updated)
	return nil
}

// kindDelete removes a user kind; ?moveTo=<kind> says where its items go
// (default: the plain base kind).
func (s *Server) kindDelete(w http.ResponseWriter, r *http.Request) error {
	err := s.Store.Update(func(f *catalog.File) error {
		k, err := f.FindKind(r.PathValue("ref"))
		if err != nil {
			return err
		}
		moveTo := r.URL.Query().Get("moveTo")
		if moveTo != "" {
			to, err := f.FindKind(moveTo)
			if err != nil {
				return err
			}
			moveTo = to.ID
		}
		return f.RemoveKind(k.ID, moveTo)
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "kinds"})
	w.WriteHeader(http.StatusNoContent)
	return nil
}
