package domain

import (
	"errors"
	"testing"
	"time"
)

func TestTrashRestoreAndPurge(t *testing.T) {
	cat := NewCatalog()
	it := cat.NewItem(KindTask, "Pull", "pull", now)
	it.Command.Line = "git pull"
	it.Tags = []string{"shop"}
	cat.Items = append(cat.Items, it)

	if _, err := cat.TrashItem(it.ID, now); err != nil || len(cat.Items) != 0 || len(cat.Trash) != 1 {
		t.Fatalf("trash: %v", err)
	}
	if cat.AliasTaken("pull") {
		t.Fatal("a trashed alias is free again")
	}
	other := cat.NewItem(KindTask, "Pull", "pull", now) // the alias is reused meanwhile
	other.Command.Line = "x"
	cat.Items = append(cat.Items, other)

	back, err := cat.RestoreItem("pull", now.Add(time.Hour))
	if err != nil || back.Alias == "pull" || back.Tags[0] != "shop" || len(cat.Trash) != 0 {
		t.Fatalf("restore: %+v %v", back, err)
	}
	if err := cat.Validate(); err != nil {
		t.Fatalf("catalog after restore: %v", err)
	}
	if _, err := cat.RestoreItem("pull", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore twice: %v", err)
	}

	cat.TrashItem(back.ID, now)
	cat.TrashItem(other.ID, now.Add(20*24*time.Hour))
	gone := cat.PurgeTrash(now.Add(31*24*time.Hour), false)
	if len(gone) != 1 || gone[0] != back.ID || len(cat.Trash) != 1 {
		t.Fatalf("purge old: %v, left %d", gone, len(cat.Trash))
	}
	if gone := cat.PurgeTrash(now, true); len(gone) != 1 || len(cat.Trash) != 0 {
		t.Fatalf("empty the trash: %v", gone)
	}
}
