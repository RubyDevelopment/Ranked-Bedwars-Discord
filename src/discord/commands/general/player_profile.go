package general

import (
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
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
	Name:        "profile",
	Description: "Displays a player's ranked profile, username, nickname, and rank.",
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
	if target == nil && helpers.OptionUserUnresolved(i, "user") {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerProfileNotRegisteredEmbed(i))
	}
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, PlayerProfileBotTargetEmbed(i))
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerProfileNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerProfileEmbed(i))
	}
	config := view.Config

	// 3. Check Privacy Settings
	isSelf := target.ID == helpers.InteractionUserID(i)
	isAdmin := helpers.MemberIsAdministrator(s, i)
	if !config.IsStatsVisible && !isSelf && !isAdmin {
		return helpers.EditInteractionEmbed(s, i, PlayerProfilePrivacyEmbed(i, config.Username))
	}

	// 4. Fetch Season Stats
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerProfileEmbed(i))
	}

	var stats *models.PlayerStatsModel
	if seasonKey != "" {
		stats, err = database.Must().Stats.GetOrCreate(i.GuildID, sdk.UintToString(view.Player.ID), seasonID)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerProfileEmbed(i))
		}
	}

	showElo := config.IsEloVisible || isSelf || isAdmin
	return helpers.EditInteractionEmbed(s, i, PlayerProfileOverviewEmbed(i, target, config, stats, seasonKey, showElo))
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorPlayerProfileEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the profile. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerProfileBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have ranked profiles. Target a registered player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerProfilePrivacyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Profile Hidden",
		Description: fmt.Sprintf("**%s** has chosen to hide their profile from other players. Only they can view their own profile.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func PlayerProfileOverviewEmbed(i *discordgo.InteractionCreate, user *discordgo.User, config *models.PlayerConfigsModel, stats *models.PlayerStatsModel, season string, showElo bool) *discordgo.MessageEmbed {
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
		Title:       fmt.Sprintf("Player Profile · %s", config.Username),
		Description: description + "\n\nUse `/stats` for a full seasonal breakdown.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(config.Username),
	})
}
