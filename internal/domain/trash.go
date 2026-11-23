package domain

import (
	"fmt"
	"slices"
	"time"
)

// TrashDays is how long deleted items can be restored.
const TrashDays = 30

// TrashedItem is a deleted item kept for restoring.
type TrashedItem struct {
	Item      Item      `json:"item"`
	DeletedAt time.Time `json:"deletedAt"`
}

// TrashItem moves an item to the trash.
func (cat *Catalog) TrashItem(id string, now time.Time) (Item, error) {
	i := slices.IndexFunc(cat.Items, func(it Item) bool { return it.ID == id })
	if i < 0 {
		return Item{}, fmt.Errorf("%w: no item %q", ErrNotFound, id)
	}
	it := cat.Items[i]
	cat.Items = slices.Delete(cat.Items, i, i+1)
	cat.Trash = append(cat.Trash, TrashedItem{Item: it, DeletedAt: now})
	return it, nil
}

// RestoreItem brings an item back from the trash. If its alias was taken in
// the meantime it gets a free one; a kind that no longer exists is dropped.
func (cat *Catalog) RestoreItem(ref string, now time.Time) (Item, error) {
	i := slices.IndexFunc(cat.Trash, func(t TrashedItem) bool { return t.Item.ID == ref || t.Item.Alias == ref })
	if i < 0 {
		return Item{}, fmt.Errorf("%w: %q is not in the trash", ErrNotFound, ref)
	}
	it := cat.Trash[i].Item
	cat.Trash = slices.Delete(cat.Trash, i, i+1)
	if cat.AliasTaken(it.Alias) {
		it.Alias = UniqueAlias(it.Alias, cat.AliasTaken)
	}
	if it.KindID != "" && !slices.ContainsFunc(cat.Kinds, func(k KindDef) bool { return k.ID == it.KindID }) {
		it.KindID = ""
	}
	it.UpdatedAt = now
	cat.Items = append(cat.Items, it)
	return it, nil
}

// PurgeTrash removes items deleted more than TrashDays ago (all of them when
// all is set) and returns their ids, so their runs and history can go too.
func (cat *Catalog) PurgeTrash(now time.Time, all bool) []string {
	var gone []string
	cat.Trash = slices.DeleteFunc(cat.Trash, func(t TrashedItem) bool {
		old := all || now.Sub(t.DeletedAt) > TrashDays*24*time.Hour
		if old {
			gone = append(gone, t.Item.ID)
		}
		return old
	})
	return gone
}
