package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	UIDMapPath = "/var/run/hive/uid-map.json"
	baseAgentUID = 2001
	proxyUserUID = 1001
)

type UIDMap struct {
	Agents         map[string]int `json:"agents"`
	ProxyUID       int            `json:"proxy_uid"`
	BaseUID        int            `json:"base_uid"`
	IptablesActive bool           `json:"iptables_active"`

	mu sync.RWMutex
}

func NewUIDMap() *UIDMap {
	return &UIDMap{
		Agents:   make(map[string]int),
		ProxyUID: proxyUserUID,
		BaseUID:  baseAgentUID,
	}
}

// AllocateUIDs assigns UIDs to agent names in alphabetical order,
// starting from BaseUID. Existing allocations are preserved.
func (u *UIDMap) AllocateUIDs(names []string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)

	for i, name := range sorted {
		if _, exists := u.Agents[name]; !exists {
			u.Agents[name] = u.BaseUID + i
		}
	}
}

// AllocateUID assigns a UID to a single agent, using the next available
// UID above all existing allocations. For runtime agent additions.
func (u *UIDMap) AllocateUID(name string) int {
	u.mu.Lock()
	defer u.mu.Unlock()

	if uid, exists := u.Agents[name]; exists {
		return uid
	}

	maxUID := u.BaseUID - 1
	for _, uid := range u.Agents {
		if uid > maxUID {
			maxUID = uid
		}
	}
	uid := maxUID + 1
	u.Agents[name] = uid
	return uid
}

// LookupByUID returns the agent name for a given UID, or empty string if not found.
func (u *UIDMap) LookupByUID(uid int) string {
	u.mu.RLock()
	defer u.mu.RUnlock()

	for name, agentUID := range u.Agents {
		if agentUID == uid {
			return name
		}
	}
	return ""
}

// LookupByName returns the UID for a given agent name, or 0 if not found.
func (u *UIDMap) LookupByName(name string) int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.Agents[name]
}

// Save writes the UID map to the given path as JSON.
func (u *UIDMap) Save(path string) error {
	u.mu.RLock()
	defer u.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create uid-map dir: %w", err)
	}
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal uid-map: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadUIDMap reads a UID map from the given path.
func LoadUIDMap(path string) (*UIDMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read uid-map: %w", err)
	}
	u := &UIDMap{}
	if err := json.Unmarshal(data, u); err != nil {
		return nil, fmt.Errorf("unmarshal uid-map: %w", err)
	}
	if u.Agents == nil {
		u.Agents = make(map[string]int)
	}
	return u, nil
}
