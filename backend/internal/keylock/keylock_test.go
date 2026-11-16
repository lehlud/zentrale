package keylock

import (
	"context"
	"errors"
	"testing"
)

func TestCanceledWaiterDoesNotBlockOtherKeysOrLeakEntries(t *testing.T) {
	var group Group
	unlock := group.Lock("slow")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := group.LockContext(ctx, "slow"); !errors.Is(err, context.Canceled) {
		if release != nil {
			release()
		}
		t.Fatal(err)
	}
	other, err := group.LockContext(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	unlock()
	unlock() // Idempotent cleanup on multiple return paths.
	if len(group.entries) != 0 {
		t.Fatal("gate entries leaked")
	}
}
