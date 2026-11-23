package domain

import (
	"fmt"
	"slices"
	"strings"
)

// KindDef is a kind the user manages. A user kind (id "k_…") has a name, a
// colour and a base: one of the five built-in kinds, which decides how its
// items behave. An entry whose id is a built-in kind ("service", …) only
// renames or recolours that built-in kind; built-in kinds cannot be removed.
type KindDef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Base  Kind   `json:"base"`
	Color string `json:"color,omitempty"`
}

// IsBuiltinKind reports whether id names one of the five built-in kinds.
func IsBuiltinKind(id string) bool { return slices.Contains(Kinds, Kind(id)) }

// NewKind returns a user kind with a fresh id.
func NewKind(name string, base Kind, color string) KindDef {
	return KindDef{ID: NewID(PrefixKind), Name: name, Base: base, Color: color}
}

// FindKind finds a kind by id or name (names compare without case). Built-in
// kinds are found by their id even without an entry, and by their default name.
func (cat Catalog) FindKind(ref string) (KindDef, error) {
	for _, k := range cat.Kinds {
		if k.ID == ref || strings.EqualFold(k.Name, ref) {
			return k, nil
		}
	}
	for _, b := range Kinds {
		if strings.EqualFold(string(b), ref) || strings.EqualFold(string(b)+"s", ref) {
			return cat.kindOf(string(b)), nil
		}
	}
	return KindDef{}, fmt.Errorf("%w: no kind %q", ErrNotFound, ref)
}

// ResolveKind turns a kind given by a user (built-in id, or a kind's id or
// name) into the item's behaviour and its user kind id ("" for a plain built-in).
func (cat Catalog) ResolveKind(ref string) (Kind, string, error) {
	if IsBuiltinKind(ref) {
		return Kind(ref), "", nil
	}
	k, err := cat.FindKind(ref)
	if err != nil {
		return "", "", fmt.Errorf("%w: kind %q is not one of %v or your own kinds", ErrInvalid, ref, Kinds)
	}
	if IsBuiltinKind(k.ID) {
		return k.Base, "", nil
	}
	return k.Base, k.ID, nil
}

// kindOf returns the entry for id; a built-in kind without an entry gets its default.
func (cat Catalog) kindOf(id string) KindDef {
	for _, k := range cat.Kinds {
		if k.ID == id {
			return k
		}
	}
	return KindDef{ID: id, Name: id, Base: Kind(id)}
}

// AllKinds lists the built-in kinds (with the user's names and colours) and
// then the user's kinds, in catalog order.
func (cat Catalog) AllKinds() []KindDef {
	out := make([]KindDef, 0, len(Kinds)+len(cat.Kinds))
	for _, b := range Kinds {
		out = append(out, cat.kindOf(string(b)))
	}
	for _, k := range cat.Kinds {
		if !IsBuiltinKind(k.ID) {
			out = append(out, k)
		}
	}
	return out
}

// KindNameTaken reports whether another kind already uses the name.
func (cat Catalog) KindNameTaken(name, exceptID string) bool {
	for _, k := range cat.AllKinds() {
		if k.ID != exceptID && strings.EqualFold(k.Name, name) {
			return true
		}
	}
	return false
}

// RemoveKind deletes a user kind; its items move to moveTo, which is
// another user kind with the same base, or "" for the plain base kind.
func (cat *Catalog) RemoveKind(id, moveTo string) error {
	if IsBuiltinKind(id) {
		return fmt.Errorf("%w: built-in kinds cannot be removed", ErrInvalid)
	}
	i := slices.IndexFunc(cat.Kinds, func(k KindDef) bool { return k.ID == id })
	if i < 0 {
		return fmt.Errorf("%w: no kind %q", ErrNotFound, id)
	}
	gone := cat.Kinds[i]
	if moveTo != "" {
		to := cat.kindOf(moveTo)
		if IsBuiltinKind(moveTo) {
			moveTo = "" // the plain base kind
		} else if !slices.ContainsFunc(cat.Kinds, func(k KindDef) bool { return k.ID == to.ID }) || to.ID == id {
			return fmt.Errorf("%w: no kind %q to move the items to", ErrNotFound, moveTo)
		}
		if to.Base != gone.Base {
			return fmt.Errorf("%w: %s items behave like %s, so they can only move to a kind based on %s", ErrInvalid, gone.Name, gone.Base, gone.Base)
		}
	}
	cat.Kinds = slices.Delete(cat.Kinds, i, i+1)
	for j := range cat.Items {
		if cat.Items[j].KindID == id {
			cat.Items[j].KindID = moveTo
		}
	}
	return nil
}

// MoveKind moves a user kind up (by < 0) or down (by > 0) among the user
// kinds; built-in kinds always come first in their fixed order.
func (cat *Catalog) MoveKind(id string, by int) error {
	if IsBuiltinKind(id) {
		return fmt.Errorf("%w: built-in kinds keep their place", ErrInvalid)
	}
	var user []int // positions of user kinds in cat.Kinds
	at := -1
	for i, k := range cat.Kinds {
		if !IsBuiltinKind(k.ID) {
			if k.ID == id {
				at = len(user)
			}
			user = append(user, i)
		}
	}
	if at < 0 {
		return fmt.Errorf("%w: no kind %q", ErrNotFound, id)
	}
	to := min(max(at+by, 0), len(user)-1)
	if to == at {
		return nil
	}
	a, b := user[at], user[to]
	k := cat.Kinds[a]
	cat.Kinds = slices.Delete(cat.Kinds, a, a+1)
	if b > a {
		b-- // the slice shrank before b
		cat.Kinds = slices.Insert(cat.Kinds, b+1, k)
	} else {
		cat.Kinds = slices.Insert(cat.Kinds, b, k)
	}
	return nil
}

func (cat Catalog) validateKinds(c *collector) {
	seen := map[string]bool{}
	names := map[string]bool{}
	for _, b := range Kinds {
		names[strings.ToLower(string(b))] = true
	}
	for i, k := range cat.Kinds {
		f := "kinds[" + itoa(i) + "]"
		if seen[k.ID] {
			c.addf(f+".id", "%q is used twice", k.ID)
		}
		seen[k.ID] = true
		if IsBuiltinKind(k.ID) {
			if k.Base != Kind(k.ID) {
				c.addf(f+".base", "the built-in kind %s keeps its own behaviour", k.ID)
			}
		} else {
			checkID(c, f+".id", PrefixKind, k.ID)
			if !slices.Contains(Kinds, k.Base) {
				c.addf(f+".base", "%q is not one of %v", k.Base, Kinds)
			}
		}
		name := strings.ToLower(strings.TrimSpace(k.Name))
		if name == "" {
			c.addf(f+".name", "is required")
		} else if names[name] && (!IsBuiltinKind(k.ID) || name != k.ID) {
			c.addf(f+".name", "%q is already the name of another kind", k.Name)
		}
		names[name] = true
		if k.Color != "" && !colorRe.MatchString(k.Color) {
			c.addf(f+".color", "%q is not a colour like #4f8cff", k.Color)
		}
	}
	for i, it := range cat.Items {
		if it.KindID == "" {
			continue
		}
		f := "items[" + itoa(i) + "].kindId"
		k := cat.kindOf(it.KindID)
		switch {
		case IsBuiltinKind(it.KindID) || !seen[it.KindID]:
			c.addf(f, "kind %q does not exist", it.KindID)
		case k.Base != it.Kind:
			c.addf(f, "the kind %s is based on %s, but the item is a %s", k.Name, k.Base, it.Kind)
		}
	}
}
