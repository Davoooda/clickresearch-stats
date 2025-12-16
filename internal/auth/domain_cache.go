package auth

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const domainCacheFile = "/tmp/domain_cache.json"

// DomainCache caches domains from stats server
type DomainCache struct {
	domains    map[string]bool
	mu         sync.RWMutex
	statsURL   string
	syncSecret string
	stopCh     chan struct{}
}

// NewDomainCache creates a new domain cache that syncs from stats server
func NewDomainCache(statsURL, syncSecret string) *DomainCache {
	dc := &DomainCache{
		domains:    make(map[string]bool),
		statsURL:   statsURL,
		syncSecret: syncSecret,
		stopCh:     make(chan struct{}),
	}

	// Try to load from stats server
	if err := dc.refresh(); err != nil {
		log.Printf("Warning: failed to load domains from stats: %v", err)
		// Fallback to file cache
		if err := dc.loadFromFile(); err != nil {
			log.Printf("Warning: no cached domains available: %v", err)
		}
	}

	// Start background refresh
	go dc.backgroundRefresh()

	return dc
}

// DomainExists checks if domain is in cache
func (dc *DomainCache) DomainExists(domain string) bool {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.domains[domain]
}

// Stop stops background refresh
func (dc *DomainCache) Stop() {
	close(dc.stopCh)
}

func (dc *DomainCache) refresh() error {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", dc.statsURL+"/api/sync/domains", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Sync-Secret", dc.syncSecret)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return err
	}

	var result struct {
		Domains []string `json:"domains"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	// Update cache
	dc.mu.Lock()
	dc.domains = make(map[string]bool)
	for _, d := range result.Domains {
		dc.domains[d] = true
	}
	dc.mu.Unlock()

	// Save to file for fallback
	dc.saveToFile(result.Domains)

	log.Printf("Domain cache refreshed: %d domains", len(result.Domains))
	return nil
}

func (dc *DomainCache) saveToFile(domains []string) {
	data, err := json.Marshal(domains)
	if err != nil {
		return
	}
	os.WriteFile(domainCacheFile, data, 0644)
}

func (dc *DomainCache) loadFromFile() error {
	data, err := os.ReadFile(domainCacheFile)
	if err != nil {
		return err
	}

	var domains []string
	if err := json.Unmarshal(data, &domains); err != nil {
		return err
	}

	dc.mu.Lock()
	dc.domains = make(map[string]bool)
	for _, d := range domains {
		dc.domains[d] = true
	}
	dc.mu.Unlock()

	log.Printf("Domain cache loaded from file: %d domains", len(domains))
	return nil
}

func (dc *DomainCache) backgroundRefresh() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := dc.refresh(); err != nil {
				log.Printf("Domain cache refresh failed (using old data): %v", err)
			}
		case <-dc.stopCh:
			return
		}
	}
}
