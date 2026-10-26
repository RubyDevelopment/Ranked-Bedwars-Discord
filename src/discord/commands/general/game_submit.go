package general

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	cmdcfg "ranked-bedwars/src/discord/config"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

// minResultDuration is how long a match must have run before a result can be submitted.
const minResultDuration = 3 * time.Minute

func init() {
	helpers.Register(GameSubmitCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameSubmitCommand Submits match results or proof for verification and ELO calculation.
var GameSubmitCommand = &types.Command{
	Name:        "submit",
	Description: "Submits match results or proof for verification and ELO calculation (game channel only).",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "screenshot",
				Description: "Screenshot showing the win / game end screen",
				Type:        discordgo.ApplicationCommandOptionAttachment,
				Required:    true,
			},
			{
				Name:        "match_id",
				Description: "Optional Match ID to submit proof for",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
			{
				Name:        "proof_url",
				Description: "Optional screenshot or video link proving match outcome",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeGameSubmitCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameSubmitCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)

	// 1. Validate the Game Channel & Participant
	game, ok := helpers.Games.ByChannel(i.ChannelID)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultNotGameChannelEmbed(i))
	}
	player, isParticipant := game.Player(actor)
	if !isParticipant {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultNotInMatchEmbed(i))
	}

	// 2. Validate the Attachment (fallback to proof_url string if attachment is absent)
	attachment := attachmentOption(i, "screenshot")
	var proofURL string
	if attachment != nil {
		if !isImageAttachment(attachment.ContentType, attachment.Filename) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultInvalidAttachmentEmbed(i))
		}
		proofURL = attachment.URL
	} else {
		proofURL = helpers.OptionString(i, "proof_url")
		if proofURL == "" {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultMissingAttachmentEmbed(i))
		}
	}

	// 3. Validate the Game State
	if game.State != helpers.GameInGame {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultNotInProgressEmbed(i))
	}
	if !game.StartedAt.IsZero() {
		if elapsed := time.Since(game.StartedAt); elapsed < minResultDuration {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultTooEarlyEmbed(i, minResultDuration-elapsed))
		}
	}
	if game.End == helpers.EndStaffPending {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultAlreadySubmittedEmbed(i))
	}
	if player.Team == "" {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultNoTeamEmbed(i))
	}

	// 4. Ranked games wait for staff, casual games are scored immediately
	if game.Mode.IsEloGainEnabled {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultStaffScoringEmbed(i, game.Number, player.Team, proofURL))
	}
	if _, err := helpers.Games.Score(actor, i.GuildID, game.ID, player.Team, helpers.ScoreOptions{}); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("score the casual match", err))
	}
	return helpers.EditInteractionEmbed(s, i, cmdcfg.SubmitResultCasualEmbed(i, game.Number, player.Team, proofURL))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// attachmentOption resolves an attachment option of the command, or nil when it was not provided.
func attachmentOption(i *discordgo.InteractionCreate, name string) *discordgo.MessageAttachment {
	data := i.ApplicationCommandData()
	for _, opt := range data.Options {
		if opt.Name != name || opt.Type != discordgo.ApplicationCommandOptionAttachment {
			continue
		}
		id, _ := opt.Value.(string)
		if data.Resolved == nil {
			return nil
		}
		return data.Resolved.Attachments[id]
	}
	return nil
}

func isImageAttachment(contentType, filename string) bool {
	if contentType == "" {
		// Fallback to filename check
		switch strings.ToLower(filepath.Ext(filename)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
			return true
		}
		return false
	}
	return strings.HasPrefix(contentType, "image/")
}
