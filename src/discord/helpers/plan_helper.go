package helpers

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

// A guild's plan comes from the Database (guilds.tier and tier_expires_at), which the dashboard fills in when a Ko-fi
// membership payment arrives and the Database clears when the paid period and its grace days run out. The bot only
// reads it: while the plan is active the bot works, once it lapses commands answer with how to renew.

// planExemptCommands stay usable without a plan, so an administrator can still set the server up and see its status.
var planExemptCommands = map[string]bool{"setup": true, "config": true, "bots": true}

// planEnforced is false in development mode and when PLAN_ENFORCEMENT=off (a switch for rolling plans out to servers
// that were registered before they existed).
func planEnforced() bool {
	return !devMode() && strings.ToLower(strings.TrimSpace(os.Getenv("PLAN_ENFORCEMENT"))) != "off"
}

func planRenewURL() string {
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("DASHBOARD_URL")), "/"); base != "" {
		return base + "/setup"
	}
	return ""
}

// planDenial is nil when the guild may use the bot right now, else the embed explaining why not. It fails open when
// the guild could not be loaded (a brief Database outage must not lock every server out).
func planDenial(gc GuildContext, cmd *types.Command, now time.Time) *discordgo.MessageEmbed {
	if !planEnforced() || gc.Guild == nil || planExemptCommands[cmd.Name] {
		return nil
	}
	g := gc.Guild
	if !g.IsEnabled {
		return &discordgo.MessageEmbed{Title: "Server Disabled", Description: "Ranked Bedwars has been disabled for this server. Contact support if you think this is a mistake.", Color: ColorError}
	}
	if g.PlanActive(now) {
		return nil
	}
	desc := "This server doesn't have an active plan, so Ranked Bedwars commands are paused."
	if g.TierExpiresAt != nil {
		desc = fmt.Sprintf("This server's plan ended on <t:%d:D>, so Ranked Bedwars commands are paused.", g.TierExpiresAt.Unix())
	}
	desc += " An administrator can renew it on Ko-fi"
	if url := planRenewURL(); url != "" {
		desc += " through the dashboard: " + url
	} else {
		desc += " through the dashboard"
	}
	desc += ". Settings and setup commands still work meanwhile."
	return &discordgo.MessageEmbed{Title: "Plan Inactive", Description: desc, Color: ColorError}
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
	return guild.HasFeature(feature, time.Now())
}

// devMode mirrors dev.Enabled (package dev imports helpers, so it cannot be called from here).
func devMode() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DEV_MODE")))
	return v == "1" || v == "true" || v == "yes"
}
