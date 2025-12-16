package cache

import (
	"encoding/json"
	"sync"
	"time"
)

type item struct {
	data      []byte
	expiresAt time.Time
}

type Cache struct {
	mu    sync.RWMutex
	items map[string]item
	ttl   time.Duration
}

func New(ttl time.Duration) *Cache {
	c := &Cache{
		items: make(map[string]item),
		ttl:   ttl,
	}
	go c.cleanup()
	return c
}

func (c *Cache) Get(key string, dest any) bool {
	c.mu.RLock()
	it, ok := c.items[key]
	c.mu.RUnlock()

	if !ok || time.Now().After(it.expiresAt) {
		return false
	}

	return json.Unmarshal(it.data, dest) == nil
}

func (c *Cache) Set(key string, val any) {
	data, err := json.Marshal(val)
	if err != nil {
		return
	}

	c.mu.Lock()
	c.items[key] = item{
		data:      data,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

func (c *Cache) cleanup() {
	for {
		time.Sleep(c.ttl)
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.items {
			if now.After(v.expiresAt) {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}
