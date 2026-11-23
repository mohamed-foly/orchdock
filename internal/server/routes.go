package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/config"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/events"
	"github.com/mohamed-foly/orchdock/internal/extterm"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/plain"
	"github.com/mohamed-foly/orchdock/internal/ports"
	"github.com/mohamed-foly/orchdock/internal/runner"
	"github.com/mohamed-foly/orchdock/web"
)

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ping", h(s.ping))
	mux.HandleFunc("GET /api/state", h(s.state))
	mux.HandleFunc("GET /api/events", s.eventStream)

	mux.HandleFunc("POST /api/items", h(s.itemCreate))
	mux.HandleFunc("PATCH /api/items/{ref}", h(s.itemPatch))
	mux.HandleFunc("DELETE /api/items/{ref}", h(s.itemDelete))
	mux.HandleFunc("POST /api/items/{ref}/duplicate", h(s.itemDuplicate))
	mux.HandleFunc("POST /api/items/{ref}/run", h(s.itemRun))
	mux.HandleFunc("POST /api/items/{ref}/stop", h(s.itemStop))
	mux.HandleFunc("POST /api/items/{ref}/restart", h(s.itemRestart))
	mux.HandleFunc("POST /api/items/{ref}/send", h(s.itemSend))
	mux.HandleFunc("POST /api/items/{ref}/terminal", h(s.itemTerminal))
	mux.HandleFunc("POST /api/items/{ref}/open", h(s.itemOpen))
	mux.HandleFunc("GET /api/items/{ref}/history", h(s.itemHistory))
	mux.HandleFunc("GET /api/items/{ref}/command", h(s.itemCommand))
	mux.HandleFunc("GET /api/runs/{runId}/log", h(s.runLog))
	mux.HandleFunc("POST /api/bulk", h(s.bulk))
	mux.HandleFunc("POST /api/attention/{ref}/clear", h(s.attentionClear))
	mux.HandleFunc("POST /api/attention/{ref}", h(s.attentionRaise))
	mux.HandleFunc("GET /api/schedule/preview", h(s.schedulePreview))
	mux.HandleFunc("GET /api/autostart", h(s.autostartGet))
	mux.HandleFunc("PUT /api/autostart", h(s.autostartPut))
	s.tagRoutes(mux)
	s.importRoutes(mux)
	s.kindRoutes(mux)
	s.iconPickRoutes(mux)
	s.organiseRoutes(mux)

	mux.HandleFunc("GET /api/ports", h(s.portsList))
	mux.HandleFunc("GET /api/ports/check", h(s.portsCheck))
	mux.HandleFunc("GET /api/config", h(s.configGet))
	mux.HandleFunc("PUT /api/config", h(s.configPut))

	mux.HandleFunc("POST /api/shell/pick-folder", h(s.shellPick(false)))
	mux.HandleFunc("POST /api/shell/pick-file", h(s.shellPick(true)))
	mux.HandleFunc("POST /api/shell/reveal", h(s.shellReveal))
	mux.HandleFunc("POST /api/shell/open-url", h(s.shellOpenURL))
	mux.HandleFunc("POST /api/ui/open", h(s.uiOpen))
	mux.HandleFunc("POST /api/quit", h(s.quit))

	mux.HandleFunc("GET /{$}", s.index)
	mux.Handle("GET /", staticFiles())
}

// index serves the page with the token filled in.
func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	b, err := fs.ReadFile(web.FS, "index.html")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(
		"default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; connect-src 'self' ws://127.0.0.1:%d ws://localhost:%d; frame-ancestors 'none'", s.Port, s.Port))
	_, _ = w.Write(bytes.ReplaceAll(b, []byte("{{TOKEN}}"), []byte(s.Token)))
}

func (s *Server) ping(w http.ResponseWriter, _ *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.Version, "pid": os.Getpid()})
	return nil
}

// itemDTO is an item with what a UI shows about it.
type itemDTO struct {
	domain.Item
	Status    runner.Status `json:"status"`
	Attention string        `json:"attention,omitempty"`
	Health    string        `json:"health,omitempty"`  // running services: starting, healthy or unhealthy
	NextRun   time.Time     `json:"nextRun,omitzero"`  // scheduled tasks: the next slot
	Favicon   string        `json:"favicon,omitempty"` // services: their own web icon, a file in the icons folder
}

func (s *Server) itemsWithStatus(cat catalog.File) []itemDTO {
	st := s.Runner.Statuses(cat.Items)
	out := make([]itemDTO, len(cat.Items))
	for i, it := range cat.Items {
		out[i] = itemDTO{Item: it, Status: st[it.ID]}
		if it.Service != nil && st[it.ID].State == runner.StateRunning {
			out[i].Health = s.healthOf(it.ID)
		}
		if it.Task != nil && it.Task.Schedule != nil {
			out[i].NextRun = s.nextRun(it.ID)
		}
		if it.Service != nil {
			out[i].Favicon = s.faviconFile(it.ID)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range out {
		out[i].Attention = s.attention[out[i].ID]
	}
	return out
}

func (s *Server) state(w http.ResponseWriter, _ *http.Request) error {
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	cfg, _ := config.Load(s.Home)
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.Version,
		"items":   s.itemsWithStatus(cat),
		"tags":    s.tagsWithIcons(cat.Tags),
		"kinds":   cat.AllKinds(),
		"views":   cat.Views,
		"trash":   len(cat.Trash),
		"config":  cfg,
	})
	return nil
}

// --- items -----------------------------------------------------------------

func (s *Server) itemCreate(w http.ResponseWriter, r *http.Request) error {
	body, err := readBody(r)
	if err != nil {
		return err
	}
	var head struct {
		Kind  domain.Kind `json:"kind"`
		Title string      `json:"title"`
		Alias string      `json:"alias"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return fmt.Errorf("%w: bad JSON: %v", domain.ErrInvalid, err)
	}
	if head.Kind == "" || head.Title == "" {
		return fmt.Errorf("%w: kind and title are required", domain.ErrInvalid)
	}
	var created domain.Item
	err = s.Store.Update(func(f *catalog.File) error {
		if head.Alias != "" && f.AliasTaken(head.Alias) {
			return fmt.Errorf("%w: alias %q is already used", domain.ErrConflict, head.Alias)
		}
		base, kindID, err := f.ResolveKind(string(head.Kind)) // a user kind may be named
		if err != nil {
			return err
		}
		now := time.Now()
		it := f.NewItem(base, head.Title, head.Alias, now)
		id, alias := it.ID, it.Alias
		if err := json.Unmarshal(body, &it); err != nil { // fields given override the defaults
			return fmt.Errorf("%w: bad JSON: %v", domain.ErrInvalid, err)
		}
		it.ID, it.Alias, it.CreatedAt, it.UpdatedAt, it.LastRunAt = id, alias, now, now, time.Time{}
		it.Kind = base
		if kindID != "" {
			it.KindID = kindID
		}
		if err := applyScheduleSpec(body, &it, now); err != nil {
			return err
		}
		f.Items = append(f.Items, it)
		created = it
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"itemId": created.ID, "change": "created"})
	writeJSON(w, http.StatusCreated, s.dtoFor(created))
	return nil
}

func (s *Server) dtoFor(it domain.Item) itemDTO {
	return s.itemsWithStatus(catalog.File{Items: []domain.Item{it}})[0]
}

func (s *Server) itemPatch(w http.ResponseWriter, r *http.Request) error {
	body, err := readBody(r)
	if err != nil {
		return err
	}
	var updated domain.Item
	err = s.Store.Update(func(f *catalog.File) error {
		found, err := runner.FindItem(f.Items, r.PathValue("ref"))
		if err != nil {
			return err
		}
		i := slices.IndexFunc(f.Items, func(it domain.Item) bool { return it.ID == found.ID })
		it := f.Items[i]
		if err := json.Unmarshal(body, &it); err != nil { // fields given overwrite
			return fmt.Errorf("%w: bad JSON: %v", domain.ErrInvalid, err)
		}
		if it.Kind != found.Kind {
			return fmt.Errorf("%w: the kind of an item cannot change", domain.ErrInvalid)
		}
		if it.Alias != found.Alias && f.AliasTaken(it.Alias) {
			return fmt.Errorf("%w: alias %q is already used", domain.ErrConflict, it.Alias)
		}
		it.ID, it.CreatedAt, it.LastRunAt, it.UpdatedAt = found.ID, found.CreatedAt, found.LastRunAt, time.Now()
		if err := applyScheduleSpec(body, &it, it.UpdatedAt); err != nil {
			return err
		}
		f.Items[i] = it
		updated = it
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"itemId": updated.ID, "change": "updated"})
	writeJSON(w, http.StatusOK, s.dtoFor(updated))
	return nil
}

func (s *Server) itemDelete(w http.ResponseWriter, r *http.Request) error {
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	it, err := runner.FindItem(cat.Items, r.PathValue("ref"))
	if err != nil {
		return err
	}
	if err := s.deleteItem(it); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteItem(it domain.Item) error {
	if _, live := s.Runner.Attachable(it.ID); live {
		return fmt.Errorf("%w: %s is running; stop it first", domain.ErrConflict, it.Alias)
	}
	err := s.Store.Update(func(f *catalog.File) error { // to the trash: restorable, runs and history kept
		_, err := f.TrashItem(it.ID, time.Now())
		return err
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.attention, it.ID)
	s.mu.Unlock()
	s.publish("item.changed", map[string]any{"itemId": it.ID, "change": "deleted"})
	return nil
}

func (s *Server) itemDuplicate(w http.ResponseWriter, r *http.Request) error {
	var copied domain.Item
	err := s.Store.Update(func(f *catalog.File) error {
		src, err := runner.FindItem(f.Items, r.PathValue("ref"))
		if err != nil {
			return err
		}
		now := time.Now()
		dup := f.NewItem(src.Kind, src.Title+" copy", src.Alias+"-copy", now)
		id, alias := dup.ID, dup.Alias
		b, _ := json.Marshal(src)
		_ = json.Unmarshal(b, &dup) // deep copy through JSON
		dup.ID, dup.Alias, dup.Title, dup.CreatedAt, dup.UpdatedAt, dup.LastRunAt = id, alias, src.Title+" copy", now, now, time.Time{}
		f.Items = append(f.Items, dup)
		copied = dup
		return nil
	})
	if err != nil {
		return err
	}
	s.publish("item.changed", map[string]any{"itemId": copied.ID, "change": "created"})
	writeJSON(w, http.StatusCreated, s.dtoFor(copied))
	return nil
}

// --- running ---------------------------------------------------------------

type runBody struct {
	Variant string `json:"variant"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Force   bool   `json:"force"`
}

func (s *Server) itemRun(w http.ResponseWriter, r *http.Request) error {
	var in runBody
	if err := decode(r, &in); err != nil {
		return err
	}
	run, err := s.Runner.Start(r.PathValue("ref"), runner.StartOptions{Variant: in.Variant, Trigger: domain.TriggerAPI, Cols: in.Cols, Rows: in.Rows})
	if err != nil {
		return err
	}
	s.runStarted(run)
	writeJSON(w, http.StatusOK, runDTO(run))
	return nil
}

func (s *Server) itemStop(w http.ResponseWriter, r *http.Request) error {
	var in runBody
	if err := decode(r, &in); err != nil {
		return err
	}
	run, err := s.Runner.Stop(r.PathValue("ref"), runner.StopOptions{Force: in.Force})
	if err != nil {
		return err
	}
	s.runEnded(run)
	writeJSON(w, http.StatusOK, runDTO(run))
	return nil
}

func (s *Server) itemRestart(w http.ResponseWriter, r *http.Request) error {
	var in runBody
	if err := decode(r, &in); err != nil {
		return err
	}
	run, err := s.Runner.Restart(r.PathValue("ref"), runner.StartOptions{Variant: in.Variant, Trigger: domain.TriggerAPI, Cols: in.Cols, Rows: in.Rows})
	if err != nil {
		return err
	}
	s.runStarted(run)
	writeJSON(w, http.StatusOK, runDTO(run))
	return nil
}

func (s *Server) itemSend(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Text  string `json:"text"`
		Enter bool   `json:"enter"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	it, err := runner.FindItem(cat.Items, r.PathValue("ref"))
	if err != nil {
		return err
	}
	run, ok := s.Runner.Attachable(it.ID)
	if !ok {
		return fmt.Errorf("%w: %s is not running", domain.ErrNotFound, it.Alias)
	}
	text := in.Text
	if in.Enter {
		text += "\r"
	}
	if err := host.SendInput(run.Dir, []byte(text)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// itemTerminal opens the item's live run in the user's own terminal window,
// attached with "orchdock attach".
func (s *Server) itemTerminal(w http.ResponseWriter, r *http.Request) error {
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	it, err := runner.FindItem(cat.Items, r.PathValue("ref"))
	if err != nil {
		return err
	}
	run, ok := s.Runner.Attachable(it.ID)
	if !ok {
		return fmt.Errorf("%w: %s is not running", domain.ErrNotFound, it.Alias)
	}
	exe := s.CLIExe
	if exe == "" {
		return errNotImplemented
	}
	open := s.OpenTerminal
	if open == nil {
		open = extterm.Open
	}
	if err := open(it.Alias, []string{exe, "--home", s.Home, "attach", run.ID}); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// runInfo is the API's view of a run.
type runInfo struct {
	RunID  string       `json:"runId"`
	ItemID string       `json:"itemId"`
	State  runner.State `json:"state"`
	Exit   *int         `json:"exitCode,omitempty"`
}

func runDTO(r runner.Run) runInfo {
	d := runInfo{RunID: r.ID, ItemID: r.ItemID, State: r.State}
	if r.Result != nil {
		d.Exit = &r.Result.ExitCode
	}
	return d
}

func (s *Server) itemHistory(w http.ResponseWriter, r *http.Request) error {
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	it, err := runner.FindItem(cat.Items, r.PathValue("ref"))
	if err != nil {
		return err
	}
	s.Runner.Reconcile(it.ID)
	lines, err := s.Runner.History(it.ID)
	if err != nil {
		return err
	}
	if lines == nil {
		lines = []domain.HistoryLine{}
	}
	writeJSON(w, http.StatusOK, lines)
	return nil
}

func (s *Server) itemCommand(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	text, err := s.Runner.CommandText(r.PathValue("ref"), q.Get("variant"), q.Get("bare") == "1" || q.Get("bare") == "true")
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
	return nil
}

// runLog serves a run's output as plain text, or as the raw terminal stream
// with ?raw=1. ?tail=n keeps the last n lines.
func (s *Server) runLog(w http.ResponseWriter, r *http.Request) error {
	runs, err := s.Runner.AllRuns()
	if err != nil {
		return err
	}
	id := r.PathValue("runId")
	i := slices.IndexFunc(runs, func(x runner.Run) bool { return x.ID == id })
	if i < 0 {
		return fmt.Errorf("%w: no run %q", domain.ErrNotFound, id)
	}
	data, err := os.ReadFile(filepath.Join(runs[i].Dir, host.LogFile))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	q := r.URL.Query()
	if q.Get("raw") != "1" {
		data = plain.Bytes(data)
	}
	if n, _ := strconv.Atoi(q.Get("tail")); n > 0 {
		data = lastLines(data, n)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
	return nil
}

func lastLines(data []byte, n int) []byte {
	trimmed := bytes.TrimRight(data, "\r\n")
	idx := len(trimmed)
	for i := 0; i < n; i++ {
		j := bytes.LastIndexByte(trimmed[:idx], '\n')
		if j < 0 {
			return data
		}
		idx = j
	}
	return data[idx+1:]
}

// --- bulk ------------------------------------------------------------------

func (s *Server) bulk(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Action string   `json:"action"`
		Refs   []string `json:"refs"`
		Tag    string   `json:"tag"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !slices.Contains([]string{"run", "stop", "tag", "untag", "delete"}, in.Action) {
		return fmt.Errorf("%w: unknown action %q", domain.ErrInvalid, in.Action)
	}
	if (in.Action == "tag" || in.Action == "untag") && !domain.ValidTag(in.Tag) {
		return fmt.Errorf("%w: %q is not a valid tag", domain.ErrInvalid, in.Tag)
	}
	type result struct {
		Ref   string `json:"ref"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	results := make([]result, len(in.Refs))
	var wg sync.WaitGroup
	for i, ref := range in.Refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.bulkOne(in.Action, ref, in.Tag)
			results[i] = result{Ref: ref, OK: err == nil}
			if err != nil {
				results[i].Error = err.Error()
			}
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
	return nil
}

func (s *Server) bulkOne(action, ref, tag string) error {
	switch action {
	case "run":
		run, err := s.Runner.Start(ref, runner.StartOptions{Trigger: domain.TriggerAPI})
		if err == nil {
			s.runStarted(run)
		}
		return err
	case "stop":
		run, err := s.Runner.Stop(ref, runner.StopOptions{})
		if err == nil {
			s.runEnded(run)
		}
		return err
	case "delete":
		cat, err := s.Store.Load()
		if err != nil {
			return err
		}
		it, err := runner.FindItem(cat.Items, ref)
		if err != nil {
			return err
		}
		return s.deleteItem(it)
	default: // tag, untag
		var id string
		err := s.Store.Update(func(f *catalog.File) error {
			found, err := runner.FindItem(f.Items, ref)
			if err != nil {
				return err
			}
			id = found.ID
			i := slices.IndexFunc(f.Items, func(it domain.Item) bool { return it.ID == found.ID })
			if action == "tag" && !slices.Contains(f.Items[i].Tags, tag) {
				f.Items[i].Tags = append(f.Items[i].Tags, tag)
			}
			if action == "untag" {
				f.Items[i].Tags = slices.DeleteFunc(f.Items[i].Tags, func(x string) bool { return x == tag })
			}
			return nil
		})
		if err == nil {
			s.publish("item.changed", map[string]any{"itemId": id, "change": "updated"})
		}
		return err
	}
}

// --- attention ---------------------------------------------------------------

func (s *Server) attentionClear(w http.ResponseWriter, r *http.Request) error {
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	it, err := runner.FindItem(cat.Items, r.PathValue("ref"))
	if err != nil {
		return err
	}
	s.mu.Lock()
	_, had := s.attention[it.ID]
	delete(s.attention, it.ID)
	s.mu.Unlock()
	if had {
		s.publish("attention.cleared", map[string]any{"itemId": it.ID})
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- ports and config ---------------------------------------------------------

func (s *Server) portsList(w http.ResponseWriter, _ *http.Request) error {
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	type row struct {
		ItemID    string `json:"itemId"`
		Alias     string `json:"alias"`
		Port      int    `json:"port"`
		Running   bool   `json:"running"`          // the item has a live run
		Listening bool   `json:"listening"`        // something accepts connections on the port
		Holder    string `json:"holder,omitempty"` // who listens, when it is not the item's own run
		Conflict  bool   `json:"conflict"`         // another item uses the same port
	}
	var rows []row
	count := map[int]int{}
	st := s.Runner.Statuses(cat.Items)
	for _, it := range cat.Items {
		if it.Service != nil && it.Service.Port > 0 {
			rows = append(rows, row{ItemID: it.ID, Alias: it.Alias, Port: it.Service.Port, Running: st[it.ID].State == runner.StateRunning})
			count[it.Service.Port]++
		}
	}
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows[i].Listening = ports.IsListening(rows[i].Port)
			rows[i].Conflict = count[rows[i].Port] > 1
			if rows[i].Listening && !rows[i].Running {
				rows[i].Holder = ports.Holder(rows[i].Port)
				if rows[i].Holder == "" {
					rows[i].Holder = "another program"
				}
			}
		}()
	}
	wg.Wait()
	slices.SortFunc(rows, func(a, b row) int { return a.Port - b.Port })
	if rows == nil {
		rows = []row{}
	}
	writeJSON(w, http.StatusOK, rows)
	return nil
}

func (s *Server) portsCheck(w http.ResponseWriter, r *http.Request) error {
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%w: port must be 1-65535", domain.ErrInvalid)
	}
	writeJSON(w, http.StatusOK, map[string]any{"port": port, "listening": ports.IsListening(port), "free": ports.IsFree(port)})
	return nil
}

func (s *Server) configGet(w http.ResponseWriter, _ *http.Request) error {
	cfg, err := config.Load(s.Home)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, cfg)
	return nil
}

func (s *Server) configPut(w http.ResponseWriter, r *http.Request) error {
	cfg := config.Default()
	if err := decode(r, &cfg); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalid, err)
	}
	if err := config.Save(s.Home, cfg); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, cfg)
	return nil
}

// --- native shell ---------------------------------------------------------------

func (s *Server) shellPick(file bool) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		if s.Shell == nil {
			return errNotImplemented
		}
		var in struct {
			Title string `json:"title"`
		}
		if err := decode(r, &in); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		var path string
		var err error
		if file {
			path, err = s.Shell.PickFile(ctx, in.Title)
		} else {
			path, err = s.Shell.PickFolder(ctx, in.Title)
		}
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": path})
		return nil
	}
}

func (s *Server) shellReveal(w http.ResponseWriter, r *http.Request) error {
	if s.Shell == nil {
		return errNotImplemented
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Path == "" {
		return fmt.Errorf("%w: path is required", domain.ErrInvalid)
	}
	if err := s.Shell.Reveal(in.Path); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) shellOpenURL(w http.ResponseWriter, r *http.Request) error {
	if s.Shell == nil {
		return errNotImplemented
	}
	var in struct {
		URL string `json:"url"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return fmt.Errorf("%w: only http and https addresses can be opened", domain.ErrInvalid)
	}
	if err := s.Shell.OpenURL(in.URL); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// itemOpen opens a link item: an address in the browser, a folder in the
// file manager. A file is only shown in its folder, never run.
func (s *Server) itemOpen(w http.ResponseWriter, r *http.Request) error {
	if s.Shell == nil {
		return errNotImplemented
	}
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	it, err := runner.FindItem(cat.Items, r.PathValue("ref"))
	if err != nil {
		return err
	}
	if it.Kind != domain.KindLink || it.Link == nil || it.Link.Target == "" {
		return fmt.Errorf("%w: %s is not a link", domain.ErrInvalid, it.Alias)
	}
	target := it.Link.Target
	switch st, statErr := os.Stat(target); {
	case strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://"):
		err = s.Shell.OpenURL(target)
	case statErr != nil:
		return fmt.Errorf("%w: %s does not exist", domain.ErrNotFound, target)
	case st.IsDir():
		err = s.Shell.OpenURL(target)
	default:
		err = s.Shell.Reveal(target)
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) uiOpen(w http.ResponseWriter, r *http.Request) error {
	if s.OpenUI == nil {
		return errNotImplemented
	}
	var in struct {
		ItemRef string `json:"itemRef"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := s.OpenUI(in.ItemRef); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) quit(w http.ResponseWriter, _ *http.Request) error {
	if s.Quit == nil {
		return errNotImplemented
	}
	w.WriteHeader(http.StatusNoContent)
	go s.Quit() // after the response is on its way
	return nil
}

// --- events ----------------------------------------------------------------------

func (s *Server) publish(typ string, data any) { s.Hub.Publish(events.Event{Type: typ, Data: data}) }

func (s *Server) runStarted(run runner.Run) {
	s.publish("run.started", map[string]any{"itemId": run.ItemID, "runId": run.ID})
	s.publish("item.changed", map[string]any{"itemId": run.ItemID, "change": "state"})
	s.watchRun(run)
}

func (s *Server) runEnded(run runner.Run) {
	reason := string(run.State)
	if run.Result != nil {
		reason = run.Result.Reason
	}
	s.publish("run.exited", map[string]any{"itemId": run.ItemID, "runId": run.ID, "reason": reason})
	s.publish("item.changed", map[string]any{"itemId": run.ItemID, "change": "state"})
}

// eventStream is a Server-Sent Events stream of everything that changes.
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.Hub.Subscribe()
	defer cancel()
	select { // wake the watcher so it polls quickly while we listen
	case s.kick <- struct{}{}:
	default:
	}
	_, _ = fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	fl.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev.Data)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			fl.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
		}
	}
}

// staticFiles serves the embedded page assets with explicit content types, so
// scripts load as modules even when the system's type registry is wrong.
func staticFiles() http.Handler {
	files := http.FileServerFS(web.FS)
	types := map[string]string{".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".svg": "image/svg+xml"}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := types[path.Ext(r.URL.Path)]; ok {
			w.Header().Set("Content-Type", t)
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}
