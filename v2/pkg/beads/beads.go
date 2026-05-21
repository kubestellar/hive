package beads

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusDone       Status = "done"
	StatusClosed     Status = "closed"
)

type BeadType string

const (
	TypeBug      BeadType = "bug"
	TypeFeature  BeadType = "feature"
	TypeTask     BeadType = "task"
	TypeEpic     BeadType = "epic"
	TypeChore    BeadType = "chore"
	TypeDecision BeadType = "decision"
)

type Priority int

const (
	PriorityCritical Priority = 0
	PriorityHigh     Priority = 1
	PriorityMedium   Priority = 2
	PriorityLow      Priority = 3
	PriorityMinor    Priority = 4
)

type Bead struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Type        BeadType          `json:"type"`
	Status      Status            `json:"status"`
	Priority    Priority          `json:"priority"`
	Actor       string            `json:"actor"`
	ExternalRef string            `json:"external_ref,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	ClosedAt    *time.Time        `json:"closed_at,omitempty"`
	DependsOn   []string          `json:"depends_on,omitempty"`
}

type Store struct {
	dir    string
	hiveID string
	beads  map[string]*Bead
	mu     sync.RWMutex
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating beads dir %s: %w", dir, err)
	}

	s := &Store{
		dir:   dir,
		beads: make(map[string]*Bead),
	}

	if err := s.load(); err != nil {
		return nil, fmt.Errorf("loading beads from %s: %w", dir, err)
	}

	return s, nil
}

// SetHiveID configures the Hive ID that will be stamped into new bead metadata.
func (s *Store) SetHiveID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hiveID = id
}

// hiveIDMetadataKey is the metadata key used to record which hive instance created a bead.
const hiveIDMetadataKey = "hive_id"

func (s *Store) Create(title string, beadType BeadType, priority Priority, actor string, externalRef string) (*Bead, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	metadata := make(map[string]string)
	if s.hiveID != "" {
		metadata[hiveIDMetadataKey] = s.hiveID
	}

	b := &Bead{
		ID:          uuid.New().String()[:12],
		Title:       title,
		Type:        beadType,
		Status:      StatusOpen,
		Priority:    priority,
		Actor:       actor,
		ExternalRef: externalRef,
		Metadata:    metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.beads[b.ID] = b
	return b, s.persist(b)
}

func (s *Store) Update(id string, fn func(b *Bead)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.beads[id]
	if !ok {
		return fmt.Errorf("bead %s not found", id)
	}

	fn(b)
	b.UpdatedAt = time.Now().UTC()

	return s.persist(b)
}

func (s *Store) Claim(id string) error {
	return s.Update(id, func(b *Bead) {
		b.Status = StatusInProgress
	})
}

func (s *Store) Close(id string) error {
	return s.Update(id, func(b *Bead) {
		now := time.Now().UTC()
		b.Status = StatusClosed
		b.ClosedAt = &now
	})
}

func (s *Store) Get(id string) (*Bead, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.beads[id]
	if !ok {
		return nil, fmt.Errorf("bead %s not found", id)
	}
	return b, nil
}

type ListFilter struct {
	Status      *Status
	Actor       *string
	ExternalRef *string
}

func (s *Store) List(filter ListFilter) []*Bead {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Bead
	for _, b := range s.beads {
		if filter.Status != nil && b.Status != *filter.Status {
			continue
		}
		if filter.Actor != nil && b.Actor != *filter.Actor {
			continue
		}
		if filter.ExternalRef != nil && b.ExternalRef != *filter.ExternalRef {
			continue
		}
		result = append(result, b)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	return result
}

func (s *Store) Ready(actor string) []*Bead {
	status := StatusOpen
	filter := ListFilter{Status: &status}
	if actor != "" {
		filter.Actor = &actor
	}
	return s.List(filter)
}

func (s *Store) FindByExternalRef(ref string) *Bead {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, b := range s.beads {
		if b.ExternalRef == ref {
			return b
		}
	}
	return nil
}

func (s *Store) AddDependency(beadID, dependsOnID string) error {
	return s.Update(beadID, func(b *Bead) {
		for _, dep := range b.DependsOn {
			if dep == dependsOnID {
				return
			}
		}
		b.DependsOn = append(b.DependsOn, dependsOnID)
	})
}

func (s *Store) SetMetadata(id, key, value string) error {
	return s.Update(id, func(b *Bead) {
		if b.Metadata == nil {
			b.Metadata = make(map[string]string)
		}
		b.Metadata[key] = value
	})
}

func (s *Store) UnsetMetadata(id, key string) error {
	return s.Update(id, func(b *Bead) {
		delete(b.Metadata, key)
	})
}

const beadsFileName = "beads.json"

func (s *Store) load() error {
	path := filepath.Join(s.dir, beadsFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var beads []*Bead
	if err := json.Unmarshal(data, &beads); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	for _, b := range beads {
		s.beads[b.ID] = b
	}
	return nil
}

func (s *Store) persist(b *Bead) error {
	var all []*Bead
	for _, b := range s.beads {
		all = append(all, b)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling beads: %w", err)
	}

	path := filepath.Join(s.dir, beadsFileName)
	return os.WriteFile(path, data, 0644)
}

func (s *Store) CloseAll(reason string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	closed := 0
	for _, b := range s.beads {
		if b.Status == StatusOpen || b.Status == StatusInProgress || b.Status == StatusBlocked {
			b.Status = StatusClosed
			b.ClosedAt = &now
			b.UpdatedAt = now
			if b.Metadata == nil {
				b.Metadata = make(map[string]string)
			}
			b.Metadata["close_reason"] = reason
			closed++
		}
	}

	if closed > 0 {
		if err := s.persist(nil); err != nil {
			return closed, err
		}
	}
	return closed, nil
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.beads)
}
