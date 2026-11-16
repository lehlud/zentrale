// Package keylock serializes transitions of the same resource without making
// unrelated accounts or resources wait. Entries disappear after the last user.
package keylock

import (
	"context"
	"sync"
)

type Group struct {
	mu      sync.Mutex
	entries map[string]*entry
}
type entry struct {
	gate  chan struct{}
	users int
}

func (group *Group) Lock(key string) func() {
	unlock, _ := group.LockContext(context.Background(), key)
	return unlock
}

func (group *Group) LockContext(ctx context.Context, key string) (func(), error) {
	group.mu.Lock()
	if group.entries == nil {
		group.entries = map[string]*entry{}
	}
	item := group.entries[key]
	if item == nil {
		item = &entry{gate: make(chan struct{}, 1)}
		group.entries[key] = item
	}
	item.users++
	group.mu.Unlock()
	removeUser := func() {
		group.mu.Lock()
		item.users--
		if item.users == 0 {
			delete(group.entries, key)
		}
		group.mu.Unlock()
	}
	select {
	case item.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-item.gate
			removeUser()
			return nil, err
		}
	case <-ctx.Done():
		removeUser()
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-item.gate
			removeUser()
		})
	}, nil
}
