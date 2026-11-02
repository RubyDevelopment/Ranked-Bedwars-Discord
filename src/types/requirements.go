package types

import "strings"

// requirementLabels name the requirement keys the bot declares. A command or event declares only the key; the bot writes this name
// into every saved slot (RequirementSlot.Label), so the dashboard shows what the bot says and keeps no list of its own.
var requirementLabels = map[string]string{
	StaffRoleKey:        "Staff role",
	RegisteredRoleKey:   "Registered player role",
	CommandLogsKey:      "Command log channel",
	ModerationAlertsKey: "Moderation alerts channel",
	GameResultsKey:      "Game results channel",
	GameAlertsKey:       "Game alerts channel",
	DecayAlertsKey:      "ELO decay alerts channel",
	"party_alerts":      "Party alerts channel",
	"chat_muted_role":   "Chat muted role",
	"vc_muted_role":     "Voice muted role",
	"queue_banned_role": "Queue banned role",
}

// RequirementLabel is the readable name of a requirement key: the one declared above, else its words with the kind of thing it
// holds spelled out (warn_logs is "Warn log channel").
func RequirementLabel(key string) string {
	if label, ok := requirementLabels[key]; ok {
		return label
	}
	words := func(s string) string {
		parts := strings.Split(s, "_")
		for i, p := range parts {
			switch p {
			case "vc":
				parts[i] = "Voice"
			case "elo":
				parts[i] = "ELO"
			case "":
			default:
				parts[i] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
		return strings.Join(parts, " ")
	}
	switch {
	case strings.HasSuffix(key, "_logs"):
		return words(strings.TrimSuffix(key, "_logs")) + " log channel"
	case strings.HasSuffix(key, "_alerts"):
		return words(strings.TrimSuffix(key, "_alerts")) + " alerts channel"
	case strings.HasSuffix(key, "_role"):
		return words(strings.TrimSuffix(key, "_role")) + " role"
	}
	return words(key)
}
