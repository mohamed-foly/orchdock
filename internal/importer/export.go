package importer

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
)

// Export writes the catalog in the import format, so a setup can move to
// another machine with `import`. Environment variable values are left empty
// unless includeEnv is set (they often hold secrets); the names stay so the
// other machine knows what to fill in. Icons the items and projects use are
// embedded. Runs, history and the trash are not exported.
func Export(cat catalog.File, home string, includeEnv bool, now time.Time) ([]byte, error) {
	out := struct {
		Format      string            `json:"format"`
		ExportedAt  time.Time         `json:"exportedAt"`
		EnvRedacted bool              `json:"envRedacted"`
		Kinds       []Kind            `json:"kinds,omitempty"`
		Tags        []domain.TagDef   `json:"tags"`
		IconData    map[string]string `json:"iconData,omitempty"`
		Items       []map[string]any  `json:"items"`
	}{Format: "orchdock-export-2", ExportedAt: now.UTC(), EnvRedacted: !includeEnv, Tags: []domain.TagDef{}, Items: []map[string]any{}}

	used := map[string]bool{}
	use := func(ic domain.Icon) {
		for _, ref := range append([]string{ic.Primary}, ic.Badges...) {
			if name, ok := strings.CutPrefix(ref, "file:"); ok {
				used[name] = true
			}
		}
	}
	kindName := map[string]string{}
	for _, k := range cat.Kinds {
		kindName[k.ID] = k.Name
		if !domain.IsBuiltinKind(k.ID) {
			out.Kinds = append(out.Kinds, Kind{Name: k.Name, Base: k.Base, Color: k.Color})
		}
	}
	for _, d := range cat.Tags {
		out.Tags = append(out.Tags, d)
		use(d.Icon)
	}
	for _, it := range cat.Items {
		if !includeEnv {
			it.Command.Env = redact(it.Command.Env)
			for i := range it.Command.Variants {
				it.Command.Variants[i].Env = redact(it.Command.Variants[i].Env)
			}
		}
		use(it.Icon)
		b, err := json.Marshal(it)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		for _, k := range []string{"id", "createdAt", "updatedAt", "lastRunAt", "projectId", "kindId"} {
			delete(m, k)
		}
		if it.KindID != "" && kindName[it.KindID] != "" {
			m["kind"] = kindName[it.KindID]
		}
		out.Items = append(out.Items, m)
	}
	names := make([]string, 0, len(used))
	for n := range used {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(home, IconDir, n))
		if err != nil {
			continue
		}
		if out.IconData == nil {
			out.IconData = map[string]string{}
		}
		out.IconData[n] = base64.StdEncoding.EncodeToString(b)
	}
	return json.MarshalIndent(out, "", "  ")
}

// redact keeps the variable names and empties the values.
func redact(env map[string]string) map[string]string {
	if len(env) == 0 {
		return env
	}
	out := make(map[string]string, len(env))
	for k := range env {
		out[k] = ""
	}
	return out
}
