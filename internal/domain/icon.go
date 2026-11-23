package domain

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Icon is a composite icon: a primary icon and up to two corner badges.
// References: builtin:<name>, emoji:<char>, file:<name>.<ext>,
// text:<1-2 chars>:<hex colour>; projects may use auto; items may use
// project, agent and favicon.
type Icon struct {
	Primary string   `json:"primary,omitempty"`
	Badges  []string `json:"badges,omitempty"`
}

var (
	builtinRe = regexp.MustCompile(`^builtin:[a-z0-9][a-z0-9-]{0,47}$`)
	fileRe    = regexp.MustCompile(`^file:[0-9a-z][0-9a-z_-]{0,79}\.(png|jpg|jpeg|ico|svg)$`)
	textRe    = regexp.MustCompile(`^text:([^:]{1,8}):#?([0-9a-fA-F]{6})$`)
	colorRe   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// ValidIconRef checks one icon reference. forProject allows "auto"; items
// allow "project", "agent" and "favicon".
func ValidIconRef(ref string, forProject bool) bool {
	switch {
	case ref == "auto":
		return forProject
	case ref == "project", ref == "agent", ref == "favicon":
		return !forProject
	case strings.HasPrefix(ref, "emoji:"):
		e := strings.TrimPrefix(ref, "emoji:")
		return e != "" && utf8.RuneCountInString(e) <= 8
	case strings.HasPrefix(ref, "text:"):
		m := textRe.FindStringSubmatch(ref)
		return m != nil && utf8.RuneCountInString(m[1]) <= 2
	}
	return builtinRe.MatchString(ref) || fileRe.MatchString(ref)
}

func (c *collector) icon(field string, ic Icon, forProject bool) {
	if ic.Primary != "" && !ValidIconRef(ic.Primary, forProject) {
		c.addf(field+".primary", "%q is not a valid icon reference", ic.Primary)
	}
	if forProject && len(ic.Badges) > 0 {
		c.addf(field+".badges", "projects have no badges")
	}
	if len(ic.Badges) > 2 {
		c.addf(field+".badges", "at most 2 badges, got %d", len(ic.Badges))
	}
	for _, b := range ic.Badges {
		if !ValidIconRef(b, false) {
			c.addf(field+".badges", "%q is not a valid icon reference", b)
		}
	}
}

// DefaultIcon is the icon a new item of kind gets.
func DefaultIcon(kind Kind) Icon {
	switch kind {
	case KindAgent:
		return Icon{Primary: "project", Badges: []string{"agent"}}
	case KindService:
		return Icon{Primary: "project", Badges: []string{"builtin:server"}}
	case KindTask:
		return Icon{Primary: "project", Badges: []string{"builtin:play"}}
	case KindShell:
		return Icon{Primary: "project", Badges: []string{"builtin:terminal"}}
	default:
		return Icon{Primary: "builtin:link"}
	}
}
