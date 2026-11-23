package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/importer"
)

func (s *Server) organiseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/export", h(s.exportCatalog))
	mux.HandleFunc("POST /api/views", h(s.viewCreate))
	mux.HandleFunc("DELETE /api/views/{id}", h(s.viewDelete))
	mux.HandleFunc("PATCH /api/views/{id}", h(s.viewPatch))
	mux.HandleFunc("POST /api/views/{id}/move", h(s.viewMove))
	mux.HandleFunc("GET /api/trash", h(s.trashList))
	mux.HandleFunc("POST /api/trash/{ref}/restore", h(s.trashRestore))
	mux.HandleFunc("DELETE /api/trash/{ref}", h(s.trashDelete))
	mux.HandleFunc("DELETE /api/trash", h(s.trashEmpty))
}

// exportCatalog sends the catalog in the import format (?includeEnv=1 keeps
// environment values, which are left out by default).
func (s *Server) exportCatalog(w http.ResponseWriter, r *http.Request) error {
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	b, err := importer.Export(cat, s.Home, r.URL.Query().Get("includeEnv") == "1", time.Now())
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="orchdock-export-%s.json"`, time.Now().Format("2006-01-02")))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b) //nolint:gosec // a JSON attachment with nosniff: downloaded, never rendered as a page
	return nil
}

// viewCreate saves a named search: {"name", "query", "group"}. A view with
// the same name is replaced.
func (s *Server) viewCreate(w http.ResponseWriter, r *http.Request) error {
	var in domain.View
	if err := decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return fmt.Errorf("%w: a view needs a name", domain.ErrInvalid)
	}
	if in.Group != "" && !slices.Contains([]string{"tag", "project", "kind", "none"}, in.Group) {
		return fmt.Errorf("%w: group is tag, kind or none", domain.ErrInvalid)
	}
	var saved domain.View
	err := s.Store.Update(func(f *catalog.File) error {
		i := slices.IndexFunc(f.Views, func(v domain.View) bool { return strings.EqualFold(v.Name, in.Name) })
		if i >= 0 {
			in.ID = f.Views[i].ID
			f.Views[i] = in
		} else {
			in.ID = domain.NewID(domain.PrefixView)
			f.Views = append(f.Views, in)
		}
		saved = in
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "views"})
	writeJSON(w, http.StatusCreated, saved)
	return nil
}

// viewPatch changes a section (saved view): {"name", "query", "group"}.
func (s *Server) viewPatch(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name  *string `json:"name"`
		Query *string `json:"query"`
		Group *string `json:"group"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	var saved domain.View
	err := s.Store.Update(func(f *catalog.File) error {
		i := slices.IndexFunc(f.Views, func(v domain.View) bool { return v.ID == r.PathValue("id") })
		if i < 0 {
			return fmt.Errorf("%w: no view %q", domain.ErrNotFound, r.PathValue("id"))
		}
		v := f.Views[i]
		if in.Name != nil {
			v.Name = strings.TrimSpace(*in.Name)
			if v.Name == "" {
				return fmt.Errorf("%w: a view needs a name", domain.ErrInvalid)
			}
		}
		if in.Query != nil {
			v.Query = *in.Query
		}
		if in.Group != nil {
			v.Group = *in.Group
		}
		f.Views[i] = v
		saved = v
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "views"})
	writeJSON(w, http.StatusOK, saved)
	return nil
}

// viewMove takes {"by": -1} (up) or {"by": 1} (down).
func (s *Server) viewMove(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		By int `json:"by"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	err := s.Store.Update(func(f *catalog.File) error {
		i := slices.IndexFunc(f.Views, func(v domain.View) bool { return v.ID == r.PathValue("id") })
		if i < 0 {
			return fmt.Errorf("%w: no view %q", domain.ErrNotFound, r.PathValue("id"))
		}
		j := min(max(i+in.By, 0), len(f.Views)-1)
		f.Views[i], f.Views[j] = f.Views[j], f.Views[i]
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "views"})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) viewDelete(w http.ResponseWriter, r *http.Request) error {
	err := s.Store.Update(func(f *catalog.File) error {
		n := len(f.Views)
		f.Views = slices.DeleteFunc(f.Views, func(v domain.View) bool { return v.ID == r.PathValue("id") })
		if len(f.Views) == n {
			return fmt.Errorf("%w: no view %q", domain.ErrNotFound, r.PathValue("id"))
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"change": "views"})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- trash ----------------------------------------------------------------------

// purgeTrash drops items deleted more than 30 days ago (or all), with their
// runs and history.
func (s *Server) purgeTrash(all bool) error {
	var gone []string
	err := s.Store.Update(func(f *catalog.File) error {
		gone = f.PurgeTrash(time.Now(), all)
		return nil
	})
	for _, id := range gone {
		_ = os.RemoveAll(filepath.Join(s.Home, "runs", id))
		_ = os.Remove(filepath.Join(s.Home, "history", id+".jsonl"))
	}
	return err
}

func (s *Server) trashList(w http.ResponseWriter, _ *http.Request) error {
	if err := s.purgeTrash(false); err != nil {
		return err
	}
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	type row struct {
		ID        string    `json:"id"`
		Alias     string    `json:"alias"`
		Title     string    `json:"title"`
		Kind      string    `json:"kind"`
		DeletedAt time.Time `json:"deletedAt"`
		Until     time.Time `json:"until"` // restorable until
	}
	rows := []row{}
	for i := len(cat.Trash) - 1; i >= 0; i-- { // newest first
		t := cat.Trash[i]
		rows = append(rows, row{t.Item.ID, t.Item.Alias, t.Item.Title, string(t.Item.Kind), t.DeletedAt, t.DeletedAt.Add(domain.TrashDays * 24 * time.Hour)})
	}
	writeJSON(w, http.StatusOK, rows)
	return nil
}

func (s *Server) trashRestore(w http.ResponseWriter, r *http.Request) error {
	var back domain.Item
	err := s.Store.Update(func(f *catalog.File) error {
		it, err := f.RestoreItem(r.PathValue("ref"), time.Now())
		back = it
		return err
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"itemId": back.ID, "change": "restored"})
	writeJSON(w, http.StatusOK, s.dtoFor(back))
	return nil
}

// trashDelete removes one item from the trash for good.
func (s *Server) trashDelete(w http.ResponseWriter, r *http.Request) error {
	ref := r.PathValue("ref")
	var id string
	err := s.Store.Update(func(f *catalog.File) error {
		i := slices.IndexFunc(f.Trash, func(t domain.TrashedItem) bool { return t.Item.ID == ref || t.Item.Alias == ref })
		if i < 0 {
			return fmt.Errorf("%w: %q is not in the trash", domain.ErrNotFound, ref)
		}
		id = f.Trash[i].Item.ID
		f.Trash = slices.Delete(f.Trash, i, i+1)
		return nil
	})
	if err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(s.Home, "runs", id))
	_ = os.Remove(filepath.Join(s.Home, "history", id+".jsonl"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) trashEmpty(w http.ResponseWriter, _ *http.Request) error {
	if err := s.purgeTrash(true); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
