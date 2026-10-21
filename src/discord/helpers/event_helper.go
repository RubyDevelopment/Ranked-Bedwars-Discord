package helpers

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
)

// Custom events are what a server has the bot do when something happens in Discord (the dashboard's Events page): post a
// ============================================================================
// Event Configuration
// ============================================================================

// The guild config's "event_configs" map mirrors "commands": one entry per
// registered event with an on/off switch and the roles, channels and categories
// the event's Requirements declare. An admin assigns them from the dashboard's
// Events page; the event reads them back with the helpers below.

const (
	eventConfigField = "event_configs"
	eventConfigTTL   = 30 * time.Second
)

type eventConfigEntry struct {
	configs map[string]types.EventConfig
	expires time.Time
}

var (
	eventConfigMu    sync.Mutex
	eventConfigCache = map[string]eventConfigEntry{}
)

// SeedEventConfigs adds a default EventConfig for every registered event the
// guild doesn't have one for yet, and keeps each entry's requirement slots in
// step with what the event declares (assigned IDs are kept).
func SeedEventConfigs(guildID string) error {
	existing, err := loadEventConfigs(guildID, false)
	if err != nil {
		return err
	}

	changed := false
	for _, ev := range EventRegistry {
		if ev == nil || ev.Name == "" {
			continue
		}
		cfg, ok := existing[ev.Name]
		if !ok {
			cfg = types.EventConfig{Name: ev.Name, Description: ev.Description, IsEnabled: !ev.Disabled || ev.System, IsSystem: ev.System}
			changed = true
		}
		if cfg.IsSystem != ev.System {
			cfg.IsSystem = ev.System
			if ev.System {
				cfg.IsEnabled = true
			}
			changed = true
		}
		if cfg.Description != ev.Description {
			cfg.Description = ev.Description
			changed = true
		}
		if reconcileEventRequirements(&cfg, ev.Requirements) {
			changed = true
		}
		existing[ev.Name] = cfg
	}
	if !changed {
		return nil
	}

	if err := database.Must().GuildConfig.SaveField(sdk.SystemActor, guildID, eventConfigField, existing); err != nil {
		return err
	}
	InvalidateEventConfig(guildID)
	return nil
}

func reconcileEventRequirements(cfg *types.EventConfig, requirements []types.Requirement) bool {
	cc := types.CommandConfig{RequiredRoles: cfg.RequiredRoles, RequiredChannels: cfg.RequiredChannels, RequiredCategories: cfg.RequiredCategories}
	changed := reconcileRequirements(&cc, requirements)
	cfg.RequiredRoles, cfg.RequiredChannels, cfg.RequiredCategories = cc.RequiredRoles, cc.RequiredChannels, cc.RequiredCategories
	return changed
}

// InvalidateEventConfig drops the cached event configs (call after an edit).
func InvalidateEventConfig(guildID string) {
	eventConfigMu.Lock()
	delete(eventConfigCache, guildID)
	eventConfigMu.Unlock()
}

func loadEventConfigs(guildID string, useCache bool) (map[string]types.EventConfig, error) {
	if useCache {
		eventConfigMu.Lock()
		e, ok := eventConfigCache[guildID]
		eventConfigMu.Unlock()
		if ok && time.Now().Before(e.expires) {
			return e.configs, nil
		}
	}

	out := map[string]types.EventConfig{}
	raw, err := database.Must().GuildConfig.Field(guildID, eventConfigField)
	if err != nil && !sdk.IsNotFound(err) {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode %s: %w", eventConfigField, err)
		}
	}
	if out == nil {
		out = map[string]types.EventConfig{}
	}

	eventConfigMu.Lock()
	eventConfigCache[guildID] = eventConfigEntry{configs: out, expires: time.Now().Add(eventConfigTTL)}
	eventConfigMu.Unlock()
	return out, nil
}

// LoadEventConfig returns the guild's config for one event (cached briefly).
func LoadEventConfig(guildID, eventName string) (types.EventConfig, bool) {
	configs, err := loadEventConfigs(guildID, true)
	if err != nil {
		return types.EventConfig{}, false
	}
	cfg, ok := configs[eventName]
	return cfg, ok
}

// EventEnabled reports whether the guild has the event switched on. An event
// without a stored config (not seeded yet) or an unreadable config stays on.
func EventEnabled(guildID, eventName string) bool {
	cfg, ok := LoadEventConfig(guildID, eventName)
	return !ok || cfg.IsEnabled || cfg.IsSystem
}

// EventRequirementID returns the snowflake an admin assigned to the event's
// requirement key of the given type ("" when unset or unknown).
func EventRequirementID(guildID, eventName string, reqType types.RequirementType, key string) string {
	cfg, ok := LoadEventConfig(guildID, eventName)
	if !ok {
		return ""
	}
	switch reqType {
	case types.ReqRole:
		return cfg.RequiredRoles[key].ID
	case types.ReqChannel:
		return cfg.RequiredChannels[key].ID
	case types.ReqCategory:
		return cfg.RequiredCategories[key].ID
	}
	return ""
}
