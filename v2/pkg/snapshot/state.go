package snapshot

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

const maxStateAge = 7 * 24 * time.Hour

type PersistedState struct {
	SavedAt          time.Time                        `json:"saved_at"`
	Agents           map[string]AgentState            `json:"agents"`
	GovernorMode     string                           `json:"governor_mode"`
	BudgetLimit      int64                            `json:"budget_limit"`
	BudgetIgnored    []string                         `json:"budget_ignored"`
	CadenceOverrides map[string]map[string]string     `json:"cadence_overrides,omitempty"`
	LastKicks        map[string]time.Time             `json:"last_kicks,omitempty"`
	BudgetSpend      int64                            `json:"budget_spend,omitempty"`
	BudgetResetAt    time.Time                        `json:"budget_reset_at,omitempty"`
	BudgetByAgent    map[string]int64                 `json:"budget_by_agent,omitempty"`
	BudgetByModel    map[string]int64                 `json:"budget_by_model,omitempty"`
	KickHistory      []GovKickEntry                   `json:"kick_history,omitempty"`
	IssueCosts       map[string]int64                 `json:"issue_costs,omitempty"`
	LastEval         time.Time                        `json:"last_eval,omitempty"`
	ACMMLevel        *int                             `json:"acmm_level,omitempty"`
}

type AgentState struct {
	Paused          bool             `json:"paused"`
	PinnedCLI       string           `json:"pinned_cli,omitempty"`
	PinnedModel     string           `json:"pinned_model,omitempty"`
	ModelOverride   string           `json:"model_override,omitempty"`
	BackendOverride string           `json:"backend_override,omitempty"`
	RestartCount    int              `json:"restart_count"`
	DisplayName     string           `json:"display_name,omitempty"`
	Description     string           `json:"description,omitempty"`
	Enabled         *bool            `json:"enabled,omitempty"`
	ClearOnKick     *bool            `json:"clear_on_kick,omitempty"`
	StaleTimeout    *int             `json:"stale_timeout,omitempty"`
	RestartStrategy string           `json:"restart_strategy,omitempty"`
	LaunchCmd       string           `json:"launch_cmd,omitempty"`
	LastKick        *time.Time       `json:"last_kick,omitempty"`
	KickHistory     []AgentKickEntry `json:"kick_history,omitempty"`
}

type GovKickEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Agent     string    `json:"agent"`
}

type AgentKickEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Agent     string    `json:"agent"`
	Snippet   string    `json:"snippet"`
}

func SaveState(path string, state *PersistedState, logger *slog.Logger) error {
	state.SavedAt = time.Now()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}

	logger.Info("state persisted", "path", path)
	return nil
}

func LoadState(path string, logger *slog.Logger) (*PersistedState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var state PersistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}

	if time.Since(state.SavedAt) > maxStateAge {
		logger.Info("state file too old, ignoring", "saved_at", state.SavedAt, "age", time.Since(state.SavedAt))
		return nil, nil
	}

	logger.Info("state restored", "saved_at", state.SavedAt, "agents", len(state.Agents))
	return &state, nil
}
