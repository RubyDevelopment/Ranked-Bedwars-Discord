package general

import (
	"fmt"
	"strings"
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
	helpers.Register(PlayerProfileCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerProfileCommand shows a player's ranked profile, username, nickname, and rank.
var PlayerProfileCommand = &types.Command{
	Name:        "player_profile",
	Description: "Displays a player's ranked profile, username, nickname, and rank.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to view (defaults to you)",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
		},
	},
	Execute: executePlayerProfileCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerProfileCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.ResolveUserOption(s, i, "user")
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, playerProfileBotTargetEmbed())
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerProfileNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerProfileEmbed())
	}
	config := view.Config

	// 3. Check Privacy Settings
	isSelf := target.ID == helpers.InteractionUserID(i)
	isAdmin := helpers.MemberIsAdministrator(s, i)
	if !config.IsStatsVisible && !isSelf && !isAdmin {
		return helpers.EditInteractionEmbed(s, i, playerProfilePrivacyEmbed(config.Username))
	}

	// 4. Fetch Season Stats
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerProfileEmbed())
	}

	var stats *models.PlayerStatsModel
	if seasonKey != "" {
		stats, err = database.Must().Stats.GetOrCreate(i.GuildID, sdk.UintToString(view.Player.ID), seasonID)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerProfileEmbed())
		}
	}

	showElo := config.IsEloVisible || isSelf || isAdmin
	return helpers.EditInteractionEmbed(s, i, playerProfileOverviewEmbed(target, config, stats, seasonKey, showElo))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerProfileEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the profile. Please contact an administrator for assistance or try again later.",
	})
}

func playerProfileBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have ranked profiles. Target a registered player instead.",
	})
}

func playerProfileNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. They must run `/player_register` before a profile is available.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func playerProfilePrivacyEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Profile Hidden",
		Description: fmt.Sprintf("**%s** has chosen to hide their profile from other players. Only they can view their own profile.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerProfileOverviewEmbed(user *discordgo.User, config *models.PlayerConfigsModel, stats *models.PlayerStatsModel, season string, showElo bool) *discordgo.MessageEmbed {
	nickname := "None"
	if config.Nickname != nil && strings.TrimSpace(*config.Nickname) != "" {
		nickname = *config.Nickname
	}

	rank := "Unranked"
	eloLine := "> **ELO:** `Hidden`"
	recordLine := "> **Record:** `No games played`"
	streakLine := "> **Win Streak:** `0`"

	if stats != nil {
		rank = stats.Rank
		if showElo {
			eloLine = fmt.Sprintf("> **ELO:** `%d` (Peak `%d`)", stats.Elo, stats.HighestElo)
		}

		total := stats.Wins + stats.Losses
		winRate := 0.0
		if total > 0 {
			winRate = float64(stats.Wins) / float64(total) * 100
		}
		recordLine = fmt.Sprintf("> **Record:** `%dW` / `%dL` (`%.1f%%` WR)", stats.Wins, stats.Losses, winRate)
		streakLine = fmt.Sprintf("> **Win Streak:** `%d` (Best `%d`)", stats.WinStreak, stats.HighestWinStreak)
	} else if showElo {
		eloLine = "> **ELO:** `N/A`"
	}

	seasonLine := "> **Season:** `None`"
	if strings.TrimSpace(season) != "" {
		seasonLine = fmt.Sprintf("> **Season:** `%s`", season)
	}

	registered := "Unknown"
	if !config.CreatedAt.IsZero() {
		registered = config.CreatedAt.UTC().Format("Jan 2, 2006")
	}

	description := strings.Join([]string{
		fmt.Sprintf("> **Discord:** <@%s>", user.ID),
		fmt.Sprintf("> **Username:** `%s`", config.Username),
		fmt.Sprintf("> **Nickname:** `%s`", nickname),
		fmt.Sprintf("> **Rank:** `%s`", rank),
		eloLine,
		recordLine,
		streakLine,
		seasonLine,
		fmt.Sprintf("> **Registered:** `%s`", registered),
	}, "\n")

	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Player Profile — %s", config.Username),
		Description: description + "\n\nUse `/player_stats` for a full seasonal breakdown.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(config.Username),
	})
}
