package domain

import "time"

// Projects are an older way of grouping items; catalogs that still have them
// are turned into tags when they are loaded (MigrateProjects).
// The Project type stays for reading those catalogs and for the folder an
// item's main tag gives a command (see Catalog.HomeOf).

// NewProject returns a project with a fresh id.
func NewProject(name, path string, now time.Time) Project {
	return Project{ID: NewID(PrefixProject), Name: name, Path: path, CreatedAt: now, UpdatedAt: now}
}

// ProjectOf turns a tag definition into the project shape commands use for
// their default folder and the {projectDir} / {projectName} placeholders.
func ProjectOf(d *TagDef) *Project {
	if d == nil {
		return nil
	}
	return &Project{Name: d.Name, Path: d.Path, Color: d.Color, Icon: d.Icon}
}
