package staff

import (
	"time"

	"github.com/bwmarrin/discordgo"

	cmdcfg "ranked-bedwars/src/discord/config"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

const (
	clearMaxAmount = 100
	clearBulkAge   = 14 * 24 * time.Hour
)

var clearMinAmount = 1.0

func init() {
	helpers.Register(ServerClearCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerClearCommand Clears and purges messages from a channel based on specified filter criteria.
var ServerClearCommand = &types.Command{
	Name:         "clear",
	Requirements: types.StaffRequirements,
	Description:  "Clears and purges messages from a channel based on specified filter criteria.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.ManageMessagesPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "amount",
				Description: "Number of messages to clear (1-100)",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    true,
				MinValue:    &clearMinAmount,
				MaxValue:    clearMaxAmount,
			},
			{
				Name:        "channel",
				Description: "Target channel to clear (defaults to current channel)",
				Type:        discordgo.ApplicationCommandOptionChannel,
				Required:    false,
			},
			{
				Name:        "user",
				Description: "Filter and delete messages from a specific user only",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
			{
				Name:        "content_type",
				Description: "Filter messages by specific content type",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Text Only", Value: "text"},
					{Name: "Images Only", Value: "images"},
					{Name: "Attachments Only", Value: "attachments"},
					{Name: "Embeds Only", Value: "embeds"},
					{Name: "Links Only", Value: "links"},
				},
			},
			{
				Name:        "override_14days",
				Description: "Override Discord's 14-day bulk delete limit (deletes individually, slower)",
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Required:    false,
			},
		},
	},
	Execute: executeServerClearCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerClearCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Amount
	amount := helpers.OptionInt(i, "amount")
	if amount < 1 {
		amount = 1
	} else if amount > clearMaxAmount {
		amount = clearMaxAmount
	}

	targetUser := helpers.OptionUser(s, i, "user")
	contentType := helpers.OptionString(i, "content_type")
	override, _ := helpers.OptionBool(i, "override_14days")

	// 2. Resolve Target Channel
	channelID := helpers.OptionChannelID(i, "channel")
	if channelID == "" {
		channelID = i.ChannelID
	}

	// 3. Verify Bot Permissions
	perms, err := s.UserChannelPermissions(s.State.User.ID, channelID)
	if err != nil || perms&discordgo.PermissionManageMessages == 0 {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.ClearMissingPermissionsEmbed(i, channelID))
	}

	// 4. Delete Messages with Filters
	deleted, skipped, err := clearChannelMessagesFiltered(s, channelID, amount, targetUser, contentType, override)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorClearEmbed(i))
	}

	if deleted == 0 && skipped == 0 {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.ClearNoMessagesEmbed(i))
	}
	if skipped > 0 && !override {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.ClearPartialEmbed(i, channelID, deleted, skipped))
	}
	return helpers.EditInteractionEmbed(s, i, cmdcfg.ClearSuccessEmbed(i, channelID, deleted))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func clearChannelMessagesFiltered(s *discordgo.Session, channelID string, amount int, targetUser *discordgo.User, contentType string, override bool) (deleted, skipped int, err error) {
	fetchLimit := amount
	if targetUser != nil || contentType != "" {
		fetchLimit = clearMaxAmount
	}

	messages, err := s.ChannelMessages(channelID, fetchLimit, "", "", "")
	if err != nil {
		return 0, 0, err
	}

	cutoff := time.Now().Add(-clearBulkAge)
	var recentIDs []string
	var oldIDs []string

	for _, message := range messages {
		// Filter by user if specified
		if targetUser != nil && (message.Author == nil || message.Author.ID != targetUser.ID) {
			continue
		}

		// Filter by content type if specified
		if contentType != "" {
			matchedType := false
			switch contentType {
			case "text":
				if message.Content != "" && len(message.Attachments) == 0 && len(message.Embeds) == 0 {
					matchedType = true
				}
			case "images":
				hasImage := false
				for _, att := range message.Attachments {
					if att.Width > 0 {
						hasImage = true
						break
					}
				}
				if hasImage {
					matchedType = true
				}
			case "attachments":
				if len(message.Attachments) > 0 {
					matchedType = true
				}
			case "embeds":
				if len(message.Embeds) > 0 {
					matchedType = true
				}
			case "links":
				if message.Content != "" && (containsString(message.Content, "http://") || containsString(message.Content, "https://")) {
					matchedType = true
				}
			}

			if !matchedType {
				continue
			}
		}

		if message.Timestamp.After(cutoff) {
			recentIDs = append(recentIDs, message.ID)
		} else {
			oldIDs = append(oldIDs, message.ID)
		}

		if len(recentIDs)+len(oldIDs) >= amount {
			break
		}
	}

	// Bulk delete execution
	if len(recentIDs) == 1 {
		if err := s.ChannelMessageDelete(channelID, recentIDs[0]); err == nil {
			deleted++
		}
	} else if len(recentIDs) > 1 {
		if err := s.ChannelMessagesBulkDelete(channelID, recentIDs); err == nil {
			deleted += len(recentIDs)
		}
	}

	if len(oldIDs) == 0 {
		return deleted, skipped, nil
	}
	if !override {
		return deleted, len(oldIDs), nil
	}

	// Rate-limited individual deletion for messages older than 14 days
	for idx, id := range oldIDs {
		if err := s.ChannelMessageDelete(channelID, id); err == nil {
			deleted++
		}
		if idx < len(oldIDs)-1 {
			time.Sleep(850 * time.Millisecond)
		}
	}
	return deleted, skipped, nil
}

func containsString(str, substr string) bool {
	return len(str) >= len(substr) && (str == substr || len(substr) == 0 || searchString(str, substr))
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
