package general

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
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
	game, ok := match.Games.ByChannel(i.ChannelID)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, SubmitResultNotGameChannelEmbed(i))
	}
	player, isParticipant := game.Player(actor)
	if !isParticipant {
		return helpers.EditInteractionEmbed(s, i, SubmitResultNotInMatchEmbed(i))
	}

	// 2. Validate the Attachment (fallback to proof_url string if attachment is absent)
	attachment := attachmentOption(i, "screenshot")
	var proofURL string
	if attachment != nil {
		if !isImageAttachment(attachment.ContentType, attachment.Filename) {
			return helpers.EditInteractionEmbed(s, i, SubmitResultInvalidAttachmentEmbed(i))
		}
		proofURL = attachment.URL
	} else {
		proofURL = helpers.OptionString(i, "proof_url")
		if proofURL == "" {
			return helpers.EditInteractionEmbed(s, i, SubmitResultMissingAttachmentEmbed(i))
		}
	}

	// 3. Validate the Game State
	if game.State != match.GameInGame {
		return helpers.EditInteractionEmbed(s, i, SubmitResultNotInProgressEmbed(i))
	}
	if !game.StartedAt.IsZero() {
		if elapsed := time.Since(game.StartedAt); elapsed < minResultDuration {
			return helpers.EditInteractionEmbed(s, i, SubmitResultTooEarlyEmbed(i, minResultDuration-elapsed))
		}
	}
	if game.End == match.EndStaffPending {
		return helpers.EditInteractionEmbed(s, i, SubmitResultAlreadySubmittedEmbed(i))
	}
	if player.Team == "" {
		return helpers.EditInteractionEmbed(s, i, SubmitResultNoTeamEmbed(i))
	}

	// 4. Ranked games wait for staff, casual games are scored immediately
	if game.Mode.Rated() {
		// The claim is recorded on the game: it moves to staff scoring and its players are free to queue again.
		if _, err := match.Games.Submit(game.ID, actor); err != nil {
			return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("submit the result", err))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.SubmitResultStaffScoringEmbed(i, game.Number, player.Team, proofURL))
	}
	if _, err := match.Games.Score(actor, i.GuildID, game.ID, player.Team, match.ScoreOptions{}); err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("score the casual match", err))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.SubmitResultCasualEmbed(i, game.Number, player.Team, proofURL))
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

// ============================================================================
// Helper functions
// ============================================================================

func SubmitResultNotGameChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of your match.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultNotInMatchEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You must be a participant in this game to submit a result.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultMissingAttachmentEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Missing Attachment",
		Description: "You must provide a screenshot of the win screen as an attachment or provide a valid proof URL.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultInvalidAttachmentEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Attachment",
		Description: "The attachment must be an image file (PNG, JPG, GIF or WebP).",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultNotInProgressEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not In Progress",
		Description: "This game is not currently in progress, so a result cannot be submitted for it.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultTooEarlyEmbed(i *discordgo.InteractionCreate, remaining time.Duration) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Too Early",
		Description: fmt.Sprintf("You must wait **%s** before submitting a result for this game.", remaining.Round(time.Second)),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultAlreadySubmittedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Submitted",
		Description: "A result has already been submitted for this game and is waiting for staff to score it.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func SubmitResultNoTeamEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Team",
		Description: "You are not assigned to a team in this game, so you cannot submit a result.",
		Thumbnail:   helpers.ThumbFlag,
	})
}
