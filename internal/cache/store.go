package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry represents a single cache entry.
type Entry struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	Version   string    `json:"version"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
	Committed bool      `json:"committed"`
}

// Store manages cache entries on disk.
type Store struct {
	dir     string // base directory (~/.ions/cache/)
	maxSize int64  // max total bytes
	mu      sync.RWMutex
	index   []Entry
	nextID  int64
}

// NewStore creates a store and loads the index from disk.
func NewStore(dir string, maxSizeGB int) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	s := &Store{
		dir:     dir,
		maxSize: int64(maxSizeGB) * 1024 * 1024 * 1024,
	}
	s.loadIndex()
	return s, nil
}

// Lookup finds a cache entry matching the given keys and version.
//
// Algorithm:
//  1. Exact match: first key + version must both match.
//  2. Prefix match: any key is a prefix of an entry's key; most recently created wins.
func (s *Store) Lookup(keys []string, version string) *Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(keys) == 0 {
		return nil
	}

	// 1. Exact match on first key + version.
	for i := range s.index {
		e := &s.index[i]
		if !e.Committed {
			continue
		}
		if e.Key == keys[0] && e.Version == version {
			return e
		}
	}

	// 2. Prefix match across all keys, most recent wins.
	var best *Entry
	for i := range s.index {
		e := &s.index[i]
		if !e.Committed {
			continue
		}
		for _, k := range keys {
			if strings.HasPrefix(e.Key, k) {
				if best == nil || e.CreatedAt.After(best.CreatedAt) {
					best = e
				}
				break
			}
		}
	}
	return best
}

// Reserve creates a pending (uncommitted) entry and returns its ID.
func (s *Store) Reserve(key, version string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	id := s.nextID

	s.index = append(s.index, Entry{
		ID:        id,
		Key:       key,
		Version:   version,
		CreatedAt: time.Now(),
	})
	s.saveIndex()
	return id, nil
}

// FindByKeyVersion finds an entry (committed or not) by exact key and version.
func (s *Store) FindByKeyVersion(key, version string) *Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.index {
		if s.index[i].Key == key && s.index[i].Version == version {
			return &s.index[i]
		}
	}
	return nil
}

// BlobPath returns the filesystem path for a cache entry's blob.
func (s *Store) BlobPath(id int64) string {
	return filepath.Join(s.dir, "blobs", fmt.Sprintf("%d", id))
}

// Commit finalizes an entry, recording its size and triggering eviction if needed.
func (s *Store) Commit(id int64, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.index {
		if s.index[i].ID == id {
			s.index[i].Size = size
			s.index[i].Committed = true
			s.evict()
			s.saveIndex()
			return nil
		}
	}
	return fmt.Errorf("cache entry %d not found", id)
}

func (s *Store) loadIndex() {
	data, err := os.ReadFile(filepath.Join(s.dir, "index.json"))
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &s.index)
	for _, e := range s.index {
		if e.ID >= s.nextID {
			s.nextID = e.ID
		}
	}
}

func (s *Store) saveIndex() {
	data, _ := json.MarshalIndent(s.index, "", "  ")
	_ = os.WriteFile(filepath.Join(s.dir, "index.json"), data, 0o644)
}

// evict removes oldest committed entries until total size is under maxSize.
func (s *Store) evict() {
	var total int64
	for _, e := range s.index {
		if e.Committed {
			total += e.Size
		}
	}
	if total <= s.maxSize {
		return
	}

	// Sort committed entries by CreatedAt ascending (oldest first).
	committed := make([]int, 0, len(s.index))
	for i, e := range s.index {
		if e.Committed {
			committed = append(committed, i)
		}
	}
	sort.Slice(committed, func(a, b int) bool {
		return s.index[committed[a]].CreatedAt.Before(s.index[committed[b]].CreatedAt)
	})

	removed := make(map[int]bool)
	for _, idx := range committed {
		if total <= s.maxSize {
			break
		}
		total -= s.index[idx].Size
		os.Remove(s.BlobPath(s.index[idx].ID))
		removed[idx] = true
	}

	if len(removed) > 0 {
		kept := make([]Entry, 0, len(s.index)-len(removed))
		for i, e := range s.index {
			if !removed[i] {
				kept = append(kept, e)
			}
		}
		s.index = kept
	}
}
