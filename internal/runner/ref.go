package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/domain"
)

// minPrefix is the shortest item id prefix accepted as a reference.
const minPrefix = 4

// FindItem resolves a reference to an item: an exact alias, an exact id, or a
// unique id prefix of at least four characters. An unknown reference is
// ErrNotFound; an ambiguous one is ErrInvalid and names the candidates.
func FindItem(items []domain.Item, ref string) (domain.Item, error) {
	if ref == "" {
		return domain.Item{}, fmt.Errorf("%w: no item given", domain.ErrInvalid)
	}
	for _, it := range items {
		if it.Alias == ref || it.ID == ref {
			return it, nil
		}
	}
	if len(ref) >= minPrefix {
		var hits []domain.Item
		for _, it := range items {
			if strings.HasPrefix(it.ID, ref) {
				hits = append(hits, it)
			}
		}
		switch len(hits) {
		case 1:
			return hits[0], nil
		case 0:
		default:
			names := make([]string, len(hits))
			for i, h := range hits {
				names[i] = h.Alias
			}
			sort.Strings(names)
			return domain.Item{}, fmt.Errorf("%w: %q matches several items: %s", domain.ErrInvalid, ref, strings.Join(names, ", "))
		}
	}
	return domain.Item{}, fmt.Errorf("%w: no item %q", domain.ErrNotFound, ref)
}
