package helpers

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	baseutility "ranked-bedwars/src/utility"
)

// A guild's plan comes from the Database (guilds.tier and tier_expires_at), which the developer sets by hand
// (approving a plan request in the dashboard) and the Database clears when the period and its grace days run out. The bot only
// reads it: while the plan is active the bot works, once it lapses commands, buttons and new games are refused with
// how to renew. Ongoing upkeep (rank decay, syncs, an already running game) is deliberately not stopped.

// ErrPlanInactive is returned when something that needs a plan (starting a game) is refused for lack of one.
var ErrPlanInactive = errors.New("this server's plan is not active")

// planExemptCommands stay usable without a plan, so an administrator can still set the server up and see its status.
var planExemptCommands = map[string]bool{"setup": true, "config": true, "bots": true}

// planEnforced is false in development mode and when PLAN_ENFORCEMENT=off (a switch for rolling plans out to servers
// that were registered before they existed).
func planEnforced() bool {
	return !devMode() && strings.ToLower(strings.TrimSpace(os.Getenv("PLAN_ENFORCEMENT"))) != "off"
}

// planGrandfathered exempts servers that were registered before PLAN_GRANDFATHER_BEFORE (an RFC 3339 date, unset = nobody)
// and never had a plan, so existing communities can be moved onto paid plans gradually instead of all at once.
func planGrandfathered(g *models.GuildsModel) bool {
	raw := strings.TrimSpace(os.Getenv("PLAN_GRANDFATHER_BEFORE"))
	if raw == "" || g.Tier != nil || g.TierExpiresAt != nil {
		return false
	}
	cutoff, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		cutoff, err = time.Parse("2006-01-02", raw)
	}
	return err == nil && g.CreatedAt.Before(cutoff)
}

// planCounts reports whether the guild may use the bot right now.
func planCounts(g *models.GuildsModel, now time.Time) bool {
	return g.IsEnabled && (g.PlanActive(now) || planGrandfathered(g))
}

// dashboardBase is the dashboard's address from DASHBOARD_URL, without a trailing slash ("" when unset).
func dashboardBase() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("DASHBOARD_URL")), "/")
}

// planPageURL is where an administrator asks for or renews a plan: the guild's plan page in the dashboard.
func planPageURL(guildID string) string {
	base := dashboardBase()
	if base == "" {
		return ""
	}
	if guildID == "" {
		return base + "/setup"
	}
	return base + "/dashboard/" + guildID + "/plan"
}

// lastKnown remembers each guild's most recent plan answer. It makes a click cheap (no Database call within
// planCacheTTL) and keeps an expired server locked, instead of failing open, while the Database is unreachable.
type lastKnown struct {
	active   bool
	disabled bool
	expires  *time.Time
	at       time.Time
}

const planCacheTTL = 30 * time.Second

var (
	planMu    sync.Mutex
	planCache = map[string]lastKnown{}
)

func rememberPlan(guildID string, g *models.GuildsModel, now time.Time) {
	planMu.Lock()
	defer planMu.Unlock()
	planCache[guildID] = lastKnown{active: planCounts(g, now), disabled: !g.IsEnabled, expires: g.TierExpiresAt, at: now}
}

func recallPlan(guildID string) (lastKnown, bool) {
	planMu.Lock()
	defer planMu.Unlock()
	k, ok := planCache[guildID]
	return k, ok
}

func inactiveEmbed(guildID string, disabled bool, expires *time.Time) *discordgo.MessageEmbed {
	if disabled {
		return &discordgo.MessageEmbed{Title: "Server Disabled", Description: "Ranked Bedwars has been disabled for this server. Contact support if you think this is a mistake.", Color: ColorError}
	}
	desc := "This server doesn't have an active plan, so Ranked Bedwars is paused."
	if expires != nil {
		desc = fmt.Sprintf("This server's plan ended on <t:%d:D>, so Ranked Bedwars is paused.", expires.Unix())
	}
	desc += " An administrator can request a plan or a renewal in the dashboard"
	if url := planPageURL(guildID); url != "" {
		desc += ": " + url
	}
	desc += ". Settings and setup commands still work meanwhile."
	return &discordgo.MessageEmbed{Title: "Plan Inactive", Description: desc, Color: ColorError}
}

// planDenial is nil when the guild may use the bot right now, else the embed explaining why not. When the guild could
// not be loaded (gc.Guild nil) it falls back to the last known answer, and lets the command through only if there is none.
func planDenial(gc GuildContext, guildID string, cmd *types.Command, now time.Time) *discordgo.MessageEmbed {
	if !planEnforced() || planExemptCommands[cmd.Name] {
		return nil
	}
	if gc.Guild != nil {
		rememberPlan(guildID, gc.Guild, now)
		if planCounts(gc.Guild, now) {
			return nil
		}
		return inactiveEmbed(guildID, !gc.Guild.IsEnabled, gc.Guild.TierExpiresAt)
	}
	if k, ok := recallPlan(guildID); ok && !k.active {
		return inactiveEmbed(guildID, k.disabled, k.expires)
	}
	return nil
}

// guildPlanOK answers "may this server use the bot now" from the cache or one Database lookup, failing open only when
// the server was never seen and cannot be loaded.
func guildPlanOK(guildID string) (ok bool, embed *discordgo.MessageEmbed) {
	if !planEnforced() || guildID == "" {
		return true, nil
	}
	now := time.Now()
	if k, found := recallPlan(guildID); found && now.Sub(k.at) < planCacheTTL {
		if k.active {
			return true, nil
		}
		return false, inactiveEmbed(guildID, k.disabled, k.expires)
	}
	guild, err := lookupGuildBySnowflake(guildID)
	if err != nil || guild == nil {
		if k, found := recallPlan(guildID); found && !k.active {
			return false, inactiveEmbed(guildID, k.disabled, k.expires)
		}
		return true, nil
	}
	rememberPlan(guildID, guild, now)
	if planCounts(guild, now) {
		return true, nil
	}
	return false, inactiveEmbed(guildID, !guild.IsEnabled, guild.TierExpiresAt)
}

// GuildPlanActive reports whether the server may use the bot now (for work that is not a command, e.g. starting a game).
func GuildPlanActive(guildID string) bool {
	ok, _ := guildPlanOK(guildID)
	return ok
}

// planBlocksComponent answers a button or menu click from a server without a plan and reports that it did.
func planBlocksComponent(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	ok, embed := guildPlanOK(i.GuildID)
	if ok {
		return false
	}
	_ = Respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Flags: discordgo.MessageFlagsEphemeral},
	})
	return true
}

// GuildHasFeature reports whether the guild's active plan includes `feature` ("automated-hosting", "custom-portal").
// Fails open when the guild cannot be loaded.
func GuildHasFeature(guildID, feature string) bool {
	if !planEnforced() {
		return true
	}
	guild, err := lookupGuildBySnowflake(guildID)
	if err != nil || guild == nil {
		return true
	}
	if planGrandfathered(guild) {
		return true
	}
	return guild.HasFeature(feature, time.Now())
}

// devMode is true when DEV_MODE is set: local testing without plan checks.
func devMode() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DEV_MODE")))
	return v == "1" || v == "true" || v == "yes"
}

// ============================================================================
// Reminders
// ============================================================================

const (
	planReminderEvery  = 6 * time.Hour
	planReminderWindow = 3 * 24 * time.Hour // warn this long before the plan ends
	planExpiredWindow  = 7 * 24 * time.Hour // tell the owner about an ended plan for this long after it
)

// remindedPlans remembers which notice each server's owner already got, per expiry date, so a restart is the only way
// to be told twice. ponytail: in memory; persist it in the Database if restarts become frequent.
var (
	remindMu       sync.Mutex
	remindedPlans  = map[string]string{}
	remindedEnding = "ending"
	remindedEnded  = "ended"
)

// HandlePlanReminders DMs each server's owner shortly before the plan ends and once after it has, until renewed.
func HandlePlanReminders(s *discordgo.Session, r *discordgo.Ready) {
	if !planEnforced() {
		return
	}
	logger := baseutility.CreateModuleLogger("PLAN_REMINDERS", "PlanReminders")
	go func() {
		for {
			runPlanReminders(s, logger, time.Now())
			time.Sleep(planReminderEvery)
		}
	}()
}

func runPlanReminders(s *discordgo.Session, logger baseutility.AppLogger, now time.Time) {
	svc := database.Get()
	if svc == nil {
		return
	}
	guilds, err := svc.Guild.ListAll("", false)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to fetch guilds for plan reminders: %v", err))
		return
	}
	for i := range guilds {
		g := &guilds[i]
		if g.TierExpiresAt == nil {
			continue
		}
		var kind, text string
		switch {
		case g.PlanActive(now) && g.Tier != nil && g.TierExpiresAt.Sub(now) <= planReminderWindow && g.TierExpiresAt.After(now):
			kind = remindedEnding
			if g.PlanCancelRequestedAt != nil {
				text = fmt.Sprintf("**%s**'s Ranked Bedwars plan ends on <t:%d:D> because you asked to cancel it. To keep the bot running, say so or request a renewal here", g.Name, g.TierExpiresAt.Unix())
			} else {
				text = fmt.Sprintf("**%s**'s Ranked Bedwars plan ends on <t:%d:D>. Plans are renewed by hand, so request a renewal here before then", g.Name, g.TierExpiresAt.Unix())
			}
		case !g.PlanActive(now) && now.Sub(*g.TierExpiresAt) <= planExpiredWindow:
			kind = remindedEnded
			text = fmt.Sprintf("**%s**'s Ranked Bedwars plan ended on <t:%d:D>, so the bot is paused there. Your configuration is kept; request a renewal here to switch it back on", g.Name, g.TierExpiresAt.Unix())
		default:
			continue
		}
		key := kind + g.TierExpiresAt.Format(time.RFC3339)
		remindMu.Lock()
		done := remindedPlans[g.SnowflakeID] == key
		remindedPlans[g.SnowflakeID] = key
		remindMu.Unlock()
		if done {
			continue
		}
		live, err := s.Guild(g.SnowflakeID)
		if err != nil || live.OwnerID == "" {
			continue // the bot is no longer in that server
		}
		dm, err := s.UserChannelCreate(live.OwnerID)
		if err != nil {
			continue
		}
		if url := planPageURL(g.SnowflakeID); url != "" {
			text += ": " + url
		} else {
			text += " in the dashboard"
		}
		if _, err := s.ChannelMessageSendEmbed(dm.ID, &discordgo.MessageEmbed{Title: "Ranked Bedwars plan", Description: text, Color: ColorError}); err != nil {
			logger.Error(fmt.Sprintf("Could not DM the owner of %s about the plan: %v", g.SnowflakeID, err))
		}
	}
}
