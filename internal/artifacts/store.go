package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Artifact represents a stored artifact.
type Artifact struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	WorkflowRunID string    `json:"workflowRunId"`
	Size          int64     `json:"size"`
	Finalized     bool      `json:"finalized"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Store manages artifact storage on disk.
type Store struct {
	dir       string
	mu        sync.RWMutex
	artifacts map[string]*Artifact // keyed by artifact ID
	idCounter atomic.Int64
}

// NewStore creates a new artifact store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{
		dir:       dir,
		artifacts: make(map[string]*Artifact),
	}
}

// Create creates a new artifact and its storage directory.
func (s *Store) Create(name, workflowRunID string) (*Artifact, error) {
	id := fmt.Sprintf("%d", s.idCounter.Add(1))

	a := &Artifact{
		ID:            id,
		Name:          name,
		WorkflowRunID: workflowRunID,
		CreatedAt:     time.Now(),
	}

	dir := filepath.Join(s.dir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}

	if err := s.writeMetadata(a); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.artifacts[id] = a
	s.mu.Unlock()

	return a, nil
}

// Get returns an artifact by ID, or nil if not found.
func (s *Store) Get(id string) *Artifact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.artifacts[id]
}

// List returns finalized artifacts, optionally filtered by workflowRunID and name.
func (s *Store) List(workflowRunID, name string) []*Artifact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Artifact
	for _, a := range s.artifacts {
		if !a.Finalized {
			continue
		}
		if workflowRunID != "" && a.WorkflowRunID != workflowRunID {
			continue
		}
		if name != "" && a.Name != name {
			continue
		}
		result = append(result, a)
	}
	return result
}

// FindByName returns the first finalized artifact matching workflowRunID and name, or nil.
func (s *Store) FindByName(workflowRunID, name string) *Artifact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, a := range s.artifacts {
		if a.Finalized && a.WorkflowRunID == workflowRunID && a.Name == name {
			return a
		}
	}
	return nil
}

// BlobPath returns the path to the artifact's blob file.
func (s *Store) BlobPath(id string) string {
	return filepath.Join(s.dir, id, "blob")
}

// BlockPath returns the path for a staged block within an artifact.
func (s *Store) BlockPath(id, blockID string) string {
	blocksDir := filepath.Join(s.dir, id, "blocks")
	os.MkdirAll(blocksDir, 0o755)
	return filepath.Join(blocksDir, blockID)
}

// Finalize marks an artifact as finalized with the given size.
func (s *Store) Finalize(id string, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.artifacts[id]
	if !ok {
		return fmt.Errorf("artifact %s not found", id)
	}

	a.Size = size
	a.Finalized = true

	s.mu.Unlock()
	err := s.writeMetadata(a)
	s.mu.Lock()

	return err
}

func (s *Store) writeMetadata(a *Artifact) error {
	data, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	path := filepath.Join(s.dir, a.ID, "metadata.json")
	return os.WriteFile(path, data, 0o644)
}
