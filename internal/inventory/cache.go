package inventory

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// ttlCache reuses a loaded value for a while. Concurrent requests for the same key share one
// load instead of each asking the registry.
type ttlCache[T any] struct {
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]*ttlEntry[T]
	// epoch counts calls to clear. A load that began before a clear may hold data from before
	// the change that caused it: its result goes to those already waiting, but it is neither
	// kept nor joined by later callers.
	epoch int
}

type ttlEntry[T any] struct {
	ready   chan struct{} // closed when the load has finished
	value   T
	err     error
	expires time.Time
	epoch   int
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, entries: map[string]*ttlEntry[T]{}}
}

// get returns the cached value of key, loading it when it is missing, expired or when fresh is
// set. The load is detached from the caller's cancellation: other callers may be waiting for it.
func (c *ttlCache[T]) get(ctx context.Context, key string, fresh bool, load func(context.Context) (T, error)) (T, error) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	loading := ok && entry.expires.IsZero() && entry.epoch == c.epoch
	if ok && (loading || (!fresh && time.Now().Before(entry.expires))) {
		c.mu.Unlock()
		select {
		case <-entry.ready:
			return entry.value, entry.err
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}
	entry = &ttlEntry[T]{ready: make(chan struct{}), epoch: c.epoch}
	c.entries[key] = entry
	c.mu.Unlock()

	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
	defer cancel()
	entry.value, entry.err = load(loadCtx)

	c.mu.Lock()
	if entry.err != nil || entry.epoch != c.epoch {
		// Errors are not cached; the next caller tries again.
		if c.entries[key] == entry {
			delete(c.entries, key)
		}
	} else {
		entry.expires = time.Now().Add(c.ttl)
		c.sweep()
	}
	c.mu.Unlock()
	close(entry.ready)
	return entry.value, entry.err
}

// sweep drops expired entries so the keys of signed-out users do not accumulate.
func (c *ttlCache[T]) sweep() {
	now := time.Now()
	for key, entry := range c.entries {
		if !entry.expires.IsZero() && now.After(entry.expires) {
			delete(c.entries, key)
		}
	}
}

func (c *ttlCache[T]) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for key, entry := range c.entries {
		if !entry.expires.IsZero() { // loads in flight stay, their waiters still need them
			delete(c.entries, key)
		}
	}
}

// lruCache is a bounded cache for values that never change, such as what a digest contains.
type lruCache[T any] struct {
	capacity int
	mu       sync.Mutex
	order    *list.List
	items    map[string]*list.Element
}

type lruItem[T any] struct {
	key   string
	value T
}

func newLRUCache[T any](capacity int) *lruCache[T] {
	return &lruCache[T]{capacity: capacity, order: list.New(), items: map[string]*list.Element{}}
}

func (c *lruCache[T]) get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		var zero T
		return zero, false
	}
	c.order.MoveToFront(element)
	return element.Value.(lruItem[T]).value, true
}

func (c *lruCache[T]) put(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		element.Value = lruItem[T]{key, value}
		c.order.MoveToFront(element)
		return
	}
	c.items[key] = c.order.PushFront(lruItem[T]{key, value})
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(lruItem[T]).key)
	}
}
