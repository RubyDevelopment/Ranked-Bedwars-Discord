package general

import (
	"fmt"
	"log"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/banner"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(PlayerStatsCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerStatsCommand displays a player's full seasonal statistics breakdown, as the banner the player
// picked among the styles of the guild's Banner Builder (or the first style they may use), or as an
// embed when the guild has made none.
var PlayerStatsCommand = &types.Command{
	Name:        "stats",
	Description: "Displays a player's seasonal statistics, win rates, and performance breakdown.",
	Cooldown:    5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Description: "User whose stats to view", Type: discordgo.ApplicationCommandOptionUser, Required: false},
			helpers.StyleOption(),
		},
	},
	Autocomplete: helpers.StyleAutocomplete("stats"),
	Execute:      executePlayerStatsCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerStatsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// Fetching avatars and drawing can take longer than the three seconds Discord allows for a first answer.
	if err := helpers.DeferSlashInteraction(s, i, false); err != nil {
		return err
	}

	invoker := invokingUser(i)
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		target = invoker
	}
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, PlayerStatsInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, PlayerStatsBotTargetEmbed(i))
	}

	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, PlayerStatsNotRegisteredEmbed(i, target))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerStatsEmbed(i))
	}
	config := view.Config

	executorID := helpers.InteractionUserID(i)
	isAdmin := helpers.MemberIsAdministrator(s, i)
	if !config.IsStatsVisible && target.ID != executorID && !isAdmin {
		return helpers.EditInteractionEmbed(s, i, PlayerStatsPrivateEmbed(i, target, config.Username))
	}

	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerStatsEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, PlayerStatsNoActiveSeasonEmbed(i, config.Username))
	}

	// Use GetOrCreate to create stats on-demand if they don't exist
	playerID := sdk.UintToString(view.Player.ID)
	stats, position, err := helpers.StatsAndRank(i.GuildID, playerID, seasonID) // creates the stats on demand if they do not exist
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerStatsEmbed(i))
	}

	sent, err := sendStatsBanner(s, i, target, invoker, config, stats, position, seasonKey, seasonID)
	if sent {
		return nil
	}
	if err != nil {
		log.Printf("stats banner for guild %s: %v", i.GuildID, err)
	}

	return helpers.EditInteractionEmbed(s, i, PlayerStatsOverviewEmbed(i, config.Username, stats))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// sendStatsBanner answers with the target's banner. sent is false (with the reason, if any) when the
// guild has no style the target may use or drawing failed: the caller then sends the embed.
func sendStatsBanner(s *discordgo.Session, i *discordgo.InteractionCreate, target, invoker *discordgo.User, config *models.PlayerConfigsModel, st *models.PlayerStatsModel, position int, season string, seasonID uint) (sent bool, err error) {
	doc := banner.LoadBanners(i.GuildID)
	viewer := banner.BannerViewer(target.ID, banner.LookupMember(s, i.GuildID, target.ID))

	// A style named in the command is the invoker's own pick: it applies to their own stats and is remembered.
	preferred := banner.ChosenBanner(config, "stats")
	if style := helpers.OptionString(i, "style"); style != "" && invoker != nil && target.ID == invoker.ID {
		for _, b := range doc.Eligible("stats", viewer) {
			if b.ID == style {
				preferred = style
				if err := banner.RememberBanner(i.GuildID, target.ID, config, "stats", style); err != nil {
					log.Printf("remember stats banner: %v", err)
				}
			}
		}
	}
	chosen := doc.Choose("stats", viewer, preferred)
	if chosen == nil {
		return false, nil
	}

	vars := banner.GuildVars(s, i.GuildID, invoker)
	for k, v := range banner.StatsVars(target, config, st, position, season, seasonID) {
		vars[k] = v
	}
	img, err := banner.RenderBanner(chosen, vars)
	if err != nil {
		return false, err
	}
	return true, helpers.EditInteractionFile(s, i, "", "stats.png", img)
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorPlayerStatsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the seasonal statistics for this player. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerStatsInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a user to view stats for. Select a registered player from the command options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerStatsBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have stats. Target a registered player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerStatsNotRegisteredEmbed(i *discordgo.InteractionCreate, user *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. They must run `/register` before stats are available.", user.ID),
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func PlayerStatsPrivateEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Stats Private",
		Description: fmt.Sprintf("<@%s> has their stats set to private. Only they can view their own stats.", user.ID),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func PlayerStatsNoActiveSeasonEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: fmt.Sprintf("This guild does not have an active season configured for **%s**. Please contact an administrator for assistance.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func PlayerStatsOverviewEmbed(i *discordgo.InteractionCreate, username string, stats *models.PlayerStatsModel) *discordgo.MessageEmbed {
	totalGames := stats.Wins + stats.Losses
	winRate := 0.0
	if totalGames > 0 {
		winRate = float64(stats.Wins) / float64(totalGames) * 100
	}
	kdr := 0.0
	if stats.Deaths > 0 {
		kdr = float64(stats.Kills) / float64(stats.Deaths)
	}

	description := fmt.Sprintf(
		"Seasonal statistics for **%s**.\n\n> **ELO:** `%d`\n> **Rank:** `%s`\n> **Wins:** `%d`\n> **Losses:** `%d`\n> **Win Rate:** `%.1f%%`\n> **Kills:** `%d`\n> **Deaths:** `%d`\n> **KDR:** `%.2f`\n> **Beds Destroyed:** `%d`\n> **Final Kills:** `%d`",
		username, stats.Elo, stats.Rank, stats.Wins, stats.Losses, winRate, stats.Kills, stats.Deaths, kdr, stats.BedsDestroyed, stats.FinalKills,
	)

	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Player Stats · %s", username),
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
