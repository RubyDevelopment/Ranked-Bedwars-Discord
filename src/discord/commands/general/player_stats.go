package general

import (
	"fmt"
	"log"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
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
	Ephemeral:   true,
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
		return helpers.EditInteractionEmbed(s, i, playerStatsInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, playerStatsBotTargetEmbed())
	}

	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerStatsNotRegisteredEmbed(target))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerStatsEmbed())
	}
	config := view.Config

	executorID := helpers.InteractionUserID(i)
	isAdmin := helpers.MemberIsAdministrator(s, i)
	if !config.IsStatsVisible && target.ID != executorID && !isAdmin {
		return helpers.EditInteractionEmbed(s, i, playerStatsPrivateEmbed(target, config.Username))
	}

	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerStatsEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, playerStatsNoActiveSeasonEmbed(config.Username))
	}

	// Use GetOrCreate to create stats on-demand if they don't exist
	playerID := sdk.UintToString(view.Player.ID)
	stats, err := database.Must().Stats.GetOrCreate(i.GuildID, playerID, seasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerStatsEmbed())
	}
	position := 0
	if rank, err := database.Must().Stats.Rank(i.GuildID, playerID, seasonID); err == nil {
		position = rank.Position
	}

	sent, err := sendStatsBanner(s, i, target, invoker, config, stats, position, seasonKey, seasonID)
	if sent {
		return nil
	}
	if err != nil {
		log.Printf("stats banner for guild %s: %v", i.GuildID, err)
	}

	return helpers.EditInteractionEmbed(s, i, playerStatsOverviewEmbed(config.Username, stats))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// sendStatsBanner answers with the target's banner. sent is false (with the reason, if any) when the
// guild has no style the target may use or drawing failed: the caller then sends the embed.
func sendStatsBanner(s *discordgo.Session, i *discordgo.InteractionCreate, target, invoker *discordgo.User, config *models.PlayerConfigsModel, st *models.PlayerStatsModel, position int, season string, seasonID uint) (sent bool, err error) {
	doc := helpers.LoadBanners(i.GuildID)
	viewer := helpers.BannerViewer(target.ID, helpers.LookupMember(s, i.GuildID, target.ID))

	// A style named in the command is the invoker's own pick: it applies to their own stats and is remembered.
	preferred := helpers.ChosenBanner(config, "stats")
	if style := helpers.OptionString(i, "style"); style != "" && invoker != nil && target.ID == invoker.ID {
		for _, b := range doc.Eligible("stats", viewer) {
			if b.ID == style {
				preferred = style
				if err := helpers.RememberBanner(i.GuildID, target.ID, config, "stats", style); err != nil {
					log.Printf("remember stats banner: %v", err)
				}
			}
		}
	}
	banner := doc.Choose("stats", viewer, preferred)
	if banner == nil {
		return false, nil
	}

	vars := helpers.GuildVars(s, i.GuildID, invoker)
	for k, v := range helpers.StatsVars(target, config, st, position, season, seasonID) {
		vars[k] = v
	}
	img, err := helpers.RenderBanner(banner, vars)
	if err != nil {
		return false, err
	}
	return true, helpers.EditInteractionFile(s, i, "", "stats.png", img)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerStatsEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the seasonal statistics for this player. Please contact an administrator for assistance or try again later.",
	})
}

func playerStatsInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a user to view stats for. Select a registered player from the command options.",
	})
}

func playerStatsBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have stats. Target a registered player instead.",
	})
}

func playerStatsNotRegisteredEmbed(user *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. They must run `/player_register` before stats are available.", user.ID),
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func playerStatsPrivateEmbed(user *discordgo.User, username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Stats Private",
		Description: fmt.Sprintf("<@%s> has their stats set to private. Only they can view their own stats.", user.ID),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerStatsNoActiveSeasonEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: fmt.Sprintf("This guild does not have an active season configured for **%s**. Please contact an administrator for assistance.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerStatsOverviewEmbed(username string, stats *models.PlayerStatsModel) *discordgo.MessageEmbed {
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
		Title:       fmt.Sprintf("Player Stats — %s", username),
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
