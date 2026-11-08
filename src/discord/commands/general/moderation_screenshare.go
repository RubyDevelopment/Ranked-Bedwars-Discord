package general

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/screenshare"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ModerationScreenshareCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationScreenshareCommand requests a screenshare for a player.
var ModerationScreenshareCommand = &types.Command{
	Name:          "screenshare",
	Requirements:  screenshare.Requirements,
	Description:   "Requests a screenshare for a player to keep competitive play fair.",
	PrefixAliases: []string{"ss"},
	Ephemeral:     true,
	Cooldown:      5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Description: "Suspected player to screenshare", Type: discordgo.ApplicationCommandOptionUser, Required: true},
			{Name: "reason", Description: "What you suspect, in a short explanation", Type: discordgo.ApplicationCommandOptionString, Required: true, MaxLength: 512},
			{Name: "evidence", Description: "Evidence upload (a video is required for SS Restricted players)", Type: discordgo.ApplicationCommandOptionAttachment},
			{Name: "evidence_link", Description: "Evidence link instead of an upload: Medal, YouTube, Streamable and so on", Type: discordgo.ApplicationCommandOptionString, MaxLength: 500},
			{Name: "timestamps", Description: "Where to look in the video, for example 0:42-1:15", Type: discordgo.ApplicationCommandOptionString, MaxLength: 200},
		},
	},
	Execute: executeModerationScreenshareCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

// executeModerationScreenshareCommand opens a screenshare ticket for the player, or, for an SS Restricted player, an
// investigation request that a screensharer has to approve first.
func executeModerationScreenshareCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if database.Get() == nil {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareDatabaseUnavailableEmbed(i))
	}

	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareBotTargetEmbed(i))
	}
	requester := helpers.InteractionUserID(i)
	if target.ID == requester {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareInputEmbed("You cannot request a screenshare on yourself."))
	}

	// Only registered players can be screenshared: the ticket shows their Minecraft username.
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, ModerationScreenshareNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareUnexpectedErrorEmbed(i))
	}
	username := target.Username
	if view.Config != nil && view.Config.Username != "" {
		username = view.Config.Username
	}

	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareInputEmbed("A reason is required. Briefly explain what you suspect."))
	}
	if r := []rune(reason); len(r) > 512 {
		reason = string(r[:512])
	}
	timestamps, problem := screenshare.NormalizeTimestamps(helpers.OptionString(i, "timestamps"))
	if problem != "" {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareInputEmbed(problem))
	}
	attachment := screenshareAttachment(i, "evidence")
	link := helpers.OptionString(i, "evidence_link")

	// The player holds the SS Restricted role while a ticket is open, so say so rather than sending a second request to review.
	if open, found := screenshare.Requests.OpenTicketOn(i.GuildID, target.ID); found {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareInputEmbed(fmt.Sprintf("<@%s> already has a screenshare ticket open: <#%s>", target.ID, open.ChannelID)))
	}

	restrictedRole := helpers.SettingRole(i.GuildID, screenshare.Command, screenshare.KeyRestricted)
	if restrictedRole != "" {
		if member, _ := helpers.FetchGuildMember(s, i.GuildID, target.ID); member != nil && slices.Contains(member.Roles, restrictedRole) {
			return submitRestrictedInvestigation(s, i, target, username, reason, timestamps, attachment, link)
		}
	}

	// A regular player: the evidence is an upload or a link, and the ticket opens straight away.
	evidenceURL, image := "", false
	switch {
	case attachment != nil:
		evidenceURL, image = attachment.URL, attachment.ContentType == "" || strings.HasPrefix(attachment.ContentType, "image/")
	case strings.TrimSpace(link) != "":
		if evidenceURL = screenshare.ParseLink(link); evidenceURL == "" {
			return helpers.EditInteractionEmbed(s, i, ModerationScreenshareInputEmbed("That evidence link is not a valid URL."))
		}
	default:
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareInputEmbed("Provide evidence: upload a file with `evidence` or paste a link in `evidence_link`."))
	}

	ticket, err := screenshare.CreateTicket(s, screenshare.TicketParams{
		GuildID: i.GuildID, Target: target, TargetName: username, RequesterID: requester, Reason: reason,
		EvidenceURL: evidenceURL, EvidenceImage: image, Timestamps: timestamps,
	})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareCreateErrorEmbed(err))
	}
	// The ticket is the answer, as a strike request is: a link to it.
	text := "Screenshare ticket opened: <#" + ticket.ChannelID + ">"
	return helpers.EditInteractionResponse(s, i, &discordgo.WebhookEdit{Content: &text})
}

// submitRestrictedInvestigation posts the request for a screensharer to approve: SS Restricted players cannot be screenshared
// directly, so the requester's video, reason and timestamps are reviewed first.
func submitRestrictedInvestigation(s *discordgo.Session, i *discordgo.InteractionCreate, target *discordgo.User, username, reason, timestamps string, attachment *discordgo.MessageAttachment, link string) error {
	evidenceURL, problem := screenshare.RestrictedEvidence(attachment, link)
	if problem != "" {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareEvidenceEmbed(problem))
	}
	// One open request per player, so reviewers are not spammed with duplicates.
	if _, pending := screenshare.Requests.PendingReview(i.GuildID, target.ID); pending {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareAlreadyPendingEmbed(target.ID))
	}
	_, channelID, err := screenshare.Review(s, i.GuildID, i.ChannelID, screenshare.TicketParams{
		GuildID: i.GuildID, Target: target, TargetName: username, RequesterID: helpers.InteractionUserID(i), Reason: reason, EvidenceURL: evidenceURL, Timestamps: timestamps,
	})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareReviewUnavailableEmbed(i))
	}
	text := "Investigation request posted for review: <#" + channelID + ">"
	return helpers.EditInteractionResponse(s, i, &discordgo.WebhookEdit{Content: &text})
}

// screenshareAttachment is the file uploaded for the named attachment option, if there is one.
func screenshareAttachment(i *discordgo.InteractionCreate, name string) *discordgo.MessageAttachment {
	if i.Type != discordgo.InteractionApplicationCommand {
		return nil
	}
	data := i.ApplicationCommandData()
	for _, option := range data.Options {
		if option.Name != name || option.Type != discordgo.ApplicationCommandOptionAttachment || data.Resolved == nil {
			continue
		}
		if id, ok := option.Value.(string); ok {
			return data.Resolved.Attachments[id]
		}
	}
	return nil
}

// ============================================================================
// Helper functions
// ============================================================================

func ModerationScreenshareDatabaseUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to submit the screenshare request because the database service is unavailable. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to request a screenshare for. Select a member from the command options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated application bot profiles cannot be screenshared. Target a real player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareInputEmbed(problem string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Request",
		Description: problem,
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareEvidenceEmbed(problem string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Video Evidence Required",
		Description: problem,
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareAlreadyPendingEmbed(targetID string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Request Already Pending",
		Description: fmt.Sprintf("An investigation request on <@%s> is already waiting for review. Wait for a screensharer to decide it before submitting another.", targetID),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareReviewUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Review Channel Unavailable",
		Description: "The investigation request could not be posted. Ask an administrator to check the SS Restricted reviews channel and the bot's permissions in it.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareCreateErrorEmbed(err error) *discordgo.MessageEmbed {
	description := "The screenshare ticket could not be created. Make sure the bot can manage channels in the screenshare category, then try again."
	if err == screenshare.ErrNoCategory {
		description = "An administrator has to set the screenshare category before screenshares can be requested."
	}
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Screenshare Not Created",
		Description: description,
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can be screenshared.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func ModerationScreenshareUnexpectedErrorEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while submitting the screenshare request. Please try again later or contact support.",
		Thumbnail:   helpers.ThumbFlag,
	})
}
