package general

import (
	"log"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	cmdcfg "ranked-bedwars/src/discord/config"
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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerStatsInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerStatsBotTargetEmbed(i))
	}

	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerStatsNotRegisteredEmbed(i, target))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerStatsEmbed(i))
	}
	config := view.Config

	executorID := helpers.InteractionUserID(i)
	isAdmin := helpers.MemberIsAdministrator(s, i)
	if !config.IsStatsVisible && target.ID != executorID && !isAdmin {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerStatsPrivateEmbed(i, target, config.Username))
	}

	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerStatsEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerStatsNoActiveSeasonEmbed(i, config.Username))
	}

	// Use GetOrCreate to create stats on-demand if they don't exist
	playerID := sdk.UintToString(view.Player.ID)
	stats, err := database.Must().Stats.GetOrCreate(i.GuildID, playerID, seasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerStatsEmbed(i))
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

	return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerStatsOverviewEmbed(i, config.Username, stats))
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
