package helpers

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// answerGuidance is the closing block of a built-in answer: what the reader can do about it. It is keyed by the embed
// title, so every command that answers "Invalid Target" or "Unexpected Error" gives the same, specific help, and a
// short answer never ends up as one line squeezed beside its thumbnail. Each entry is a list of short lines.
var answerGuidance = map[string][]string{
	// Players and registration
	"Registration Required":    {"Run `/register` to link your Minecraft account.", "Then run this command again."},
	"Not Registered":           {"Run `/register` to link your Minecraft account first."},
	"Registration Unavailable": {"A guild administrator must add at least one rank in the dashboard under Matchmaking › Rank Thresholds."},
	"Already Registered":       {"Your profile already exists in this guild.", "Use `/rename` to change the linked username, or `/profile` to view it."},
	"Already Linked":           {"That account is already attached to the profile.", "Use `/profile` to review the linked username."},
	"Player Not Registered":    {"They must run `/register` to link a Minecraft account first.", "Staff can use `/forceregister` to register them."},
	"Player Not Found":         {"Check the spelling, or mention the player instead of typing the name."},
	"Profile Not Found":        {"Run `/sync` (or `/forcesync` for another player) to rebuild the profile.", "Contact staff if it keeps happening."},
	"Member Not Found":         {"Check that the person is still in this guild.", "They may have left or been removed."},
	"Invalid Target":           {"Select a member from the command's user option.", "Start typing a name, or paste their Discord ID, to find them."},
	"Invalid User":             {"Select a member who is in this guild.", "Paste a valid Discord ID if they are not in the list."},
	"Invalid User ID":          {"Turn on Developer Mode in Discord, then right-click the user and choose **Copy User ID**.", "IDs are 17 to 20 digits long."},
	"Invalid Username":         {"Minecraft usernames are 3 to 16 characters long.", "Only letters, numbers and underscores are allowed."},
	"Username Taken":           {"Another player in this guild already uses that username.", "Contact staff if the account is yours."},
	"Account Not Found":        {"Check the spelling of the Minecraft username.", "Use the exact name shown in your Minecraft launcher."},
	"Verification Unavailable": {"The account verification service is not reachable right now.", "Try again in a few minutes."},
	"Invalid Nickname":         {"Nicknames are 3 to 16 characters long.", "Only letters, numbers and underscores are allowed."},
	"Nickname Taken":           {"Another player in this guild already uses that nickname.", "Try a different one."},
	"Nickname Already Set":     {"Your nickname already has that value.", "Choose a different nickname if you want a change."},
	"No Nickname Set":          {"Set one with `/nickname` first.", "Then you can remove it again."},
	"Premium Username":         {"Choose a nickname that is not a registered premium Minecraft name."},
	"Profile Hidden":           {"Only the owner can see this profile.", "They can change it with `/settings toggle`."},
	"Stats Hidden":             {"Only the owner can see these stats.", "They can change it with `/settings toggle`."},
	"Stats Private":            {"Only the owner can see these stats.", "They can change it with `/settings toggle`."},
	"No Stats Found":           {"The player needs to finish a ranked game this season first.", "Check again after their next game."},
	"Already Claimed":          {"Starting ELO can only be claimed once per season.", "Use `/stats` to see your current ELO."},
	"Claiming Disabled":        {"This guild has switched off starting ELO claims.", "Ask staff if you think this is a mistake."},
	"No Starting ELO Perk":     {"Starting ELO comes from perks tied to roles.", "Check that you hold the role the perk needs, then run `/claim` again."},
	"ELO Decay Incoming":       {"Play a ranked game before the timer ends to keep your ELO.", "Use `/decays` to review your decay history."},
	"ELO Decayed":              {"Play ranked games regularly to avoid further decay.", "Use `/decays` to review your decay history."},
	"No Games To Wipe":         {"Pick an earlier game number, or wipe the whole season instead."},
	"No Wipe Found":            {"Only wiped stats can be restored.", "Check the wipe ID and try again."},
	"Already Restored":         {"That wipe was already undone, so nothing changed.", "Use `/stats` to review the player's current numbers."},

	// Seasons, ranks and guild setup
	"No Active Season":                 {"A guild administrator must start a season in the dashboard under Matchmaking › Season Periods.", "Until then, stats and ELO changes are unavailable."},
	"No Configured Ranks":              {"Add rank tiers in the dashboard under Matchmaking › Rank Thresholds."},
	"No Ranks Configured":              {"Add rank tiers in the dashboard under Matchmaking › Rank Thresholds."},
	"No Registered Bots":               {"Add game bots in the dashboard under Matchmaking › Automated Bots."},
	"No Registered Maps":               {"Add maps in the dashboard under Matchmaking › Map Selections."},
	"LFG Not Configured":               {"A guild administrator must set the LFG channel and role in the dashboard.", "Until then, ask for games in your usual queue channel."},
	"Ladder Not Configured":            {"A guild administrator must add ladder steps in the dashboard under Moderation."},
	"Guild Setup Failed":               {"Give the bot Manage Roles, Manage Channels and Manage Server.", "Then run `/setup init` again."},
	"Guild Sync Failed":                {"Give the bot Manage Roles, Manage Channels and Manage Server.", "Then run `/setup sync` again."},
	"Command Not Set Up":               {"Run `/config setup` to create the command's stored configuration.", "Then try again."},
	"Optional Configuration Available": {"The command works without it.", "Set the optional items in the dashboard under Commands to unlock more."},
	"Unknown Command":                  {"Check the command name in the slash menu.", "Run `/config` without options to see what can be configured."},
	"Unknown Requirement Key":          {"Run `/config` without options to see the keys the command accepts."},
	"Unknown Subcommand":               {"Run `/config` without arguments to see the available routes."},
	"Unknown Setting":                  {"Choose one of the settings from the command's option list."},
	"Unknown Statistic":                {"Choose a statistic from the command's option list."},
	"Invalid Key":                      {"Provide a key with at least one character.", "Run `/config` without options to see the valid keys."},
	"Invalid Prefix":                   {"Choose a prefix of 1 to 5 characters, for example `!` or `rb!`."},
	"Missing Value":                    {"Provide a value for the option you chose.", "Run the command again with every required option filled in."},
	"Nothing To Change":                {"The new value matches the current one.", "Change it before saving."},

	// Games, queues and parties
	"Game Not Found":           {"Check the game number and try again.", "Use `/games` to list recent game numbers."},
	"Game Not Voided":          {"Only voided games can be restored.", "Use `/viewgame` to check the game's current state."},
	"Hosted Game Not Found":    {"Use `/host list` to see the games that are running.", "Check the game number and try again."},
	"No Game":                  {"Run the command inside a game channel, or provide a game number.", "Use `/games` to look up recent game numbers."},
	"No Game or Queue":         {"Run it in a game text channel to see that game.", "Or join a queue voice channel to see its status."},
	"Not a Game Channel":       {"Run it in the text channel of your game.", "Game channels are created when the game starts."},
	"Not a Queue Channel":      {"Join one of the guild's queue voice channels.", "Then run the command again."},
	"Not in Match":             {"Only players in the game can do this.", "Ask staff to help if you should be in it."},
	"Not in Game":              {"Join your team voice channel while a game is running.", "Then run the command again."},
	"Not in Voice":             {"Connect to your team voice channel first.", "Then run the command again."},
	"Wrong Channel":            {"Join your assigned team voice channel and try again."},
	"Already in Match":         {"That player is already part of this game.", "Pick someone who is not playing in it."},
	"Not In Progress":          {"Results can only be submitted while a game is in progress.", "Check the game's status with `/viewgame`."},
	"Already Submitted":        {"A result is already waiting for staff to score.", "Staff will review the proof and finish the game."},
	"Already Voided":           {"That game is already voided, so no ELO or stats are recorded for it.", "Use `/unvoid` if it was voided by mistake."},
	"Too Early":                {"Wait until the minimum time since the game started has passed.", "The time left is shown above."},
	"No Team":                  {"Only players assigned to a team can submit a result.", "Ask staff if you were substituted in."},
	"Missing Attachment":       {"Attach a screenshot of the win screen to your message.", "You can also paste a direct link to the image."},
	"Invalid Attachment":       {"Attach an image file: PNG, JPG, GIF or WebP.", "Videos and other file types are not accepted."},
	"Invalid MVP":              {"Select the MVP from the command's player option.", "The MVP must be a player in the game."},
	"Invalid State":            {"The game must be finished (completed or voided) first.", "Use `/score` for games that are still in progress."},
	"Invalid Selection":        {"Pick a player who is still in the pool.", "Players already drafted cannot be picked again."},
	"No Active Draft":          {"A draft only runs while captains are picking teams.", "Check the game channel for the draft panel."},
	"Draft Complete":           {"Every pick has been made, so nothing more can be drafted."},
	"Queue Entry Denied":       {"Check the reason listed above and fix it.", "Then rejoin the queue."},
	"Queues Closed":            {"Staff have closed ranked queues for now.", "Games start again when queues reopen."},
	"Queues Paused":            {"Staff have paused ranked queues for now.", "Games start again when queues reopen."},
	"Party ELO Limit":          {"Party members must be within the guild's ELO gap.", "Invite players closer to your ELO."},
	"Not in a Party":           {"Create one with `/party create`.", "Or accept a pending invitation."},
	"Not Your Invitation":      {"Only the invited player can answer an invitation.", "Ask the leader to send you your own."},
	"Removed from Party":       {"Create or join another party to keep queueing together."},
	"Party Disbanded":          {"Create a new party any time with `/party create`."},
	"Invitation Declined":      {"Ask the leader for a fresh invitation if you change your mind."},
	"Invites Closed":           {"Reopen invites with `/party invites` when you want more players."},
	"Invites Open":             {"Invite players with `/party invite`."},
	"Left Party":               {"Create a new party with `/party create`, or accept another invitation."},
	"You Are the Party Leader": {"Invite players with `/party invite`.", "Disband the party with `/party disband`."},

	// Moderation and staff
	"Reason Required":            {"Add a clear explanation in the `reason` option.", "Reasons are recorded in the player's history."},
	"Hierarchy Error":            {"Move the bot's role above the member's highest role in Server Settings › Roles.", "Or ask a higher-ranked moderator to handle it."},
	"Punishment Already Active":  {"Check the existing punishment with `/punishments`.", "Clear it first if a new one is needed."},
	"Punishment Not Active":      {"Verify the player's status with `/punishments`."},
	"Punishment Not Found":       {"Double-check the ID from `/punishments`."},
	"No Active Punishments":      {"Their full history is available with `/punishments`."},
	"Strike Not Found":           {"Double-check the ID from `/strikes`."},
	"No Strikes Found":           {"Review the player's history with `/strikes`."},
	"Role Already Assigned":      {"No change is needed.", "Use `/role list` to see their roles."},
	"Role Not Assigned":          {"Use `/role list` to see their current roles."},
	"Role Not Manageable":        {"Move the bot's role above that role in Server Settings › Roles."},
	"No Members Found":           {"Mention at least one member, paste their IDs or pick a role filter."},
	"No Messages Found":          {"Adjust or remove the filters.", "Or pick a channel with recent messages."},
	"Messages Partially Cleared": {"Messages older than 14 days cannot be bulk deleted.", "Run it again with `override_14days: True` to remove them one by one."},
	"Invalid Time Filter":        {"Use values like `7d`, `2w` or `1m`, or leave the filter empty to see everything."},
	"Page Empty":                 {"Try a lower page number.", "Use the buttons under the list to move between pages."},
	"Invalid Link":               {"Right-click the message and choose **Copy Message Link**.", "Paste the full link, starting with `https://discord.com/channels/`."},
	"Restricted Destination":     {"Only links from this guild work.", "Copy the message link from a channel in this guild."},
	"Invalid Channel":            {"Choose a text channel the bot can see and write in."},
	"Invalid Code":               {"Check the code and enter it exactly as shown.", "Codes expire, so request a fresh one if it has been a while."},
	"Nothing To Verify":          {"Nothing needs checking right now.", "Run the command again after something changes."},

	// Tickets
	"Not A Ticket":         {"Run this command inside an open ticket thread."},
	"Not Claimed":          {"Claim the ticket first with `/ticket claim`."},
	"Panel Not Found":      {"Ask staff to publish a fresh panel with `/ticket panel`.", "Then open your ticket from the new panel."},
	"Panel Outdated":       {"Ask staff to publish a fresh panel with `/ticket panel`.", "Then open your ticket from the new panel."},
	"Ticket Limit Reached": {"Continue in one of your open tickets, or close one before opening another."},
	"Ticket Not Created":   {"Give the bot **Create Private Threads** in the panel's channel.", "Then try again, or contact staff."},

	// Permissions and general failures
	"Permission Denied":        {"You need a role that is allowed to use this command.", "Ask a guild administrator to review the command's roles in the dashboard."},
	"Staff Only":               {"Ask a staff member to run this for you."},
	"Missing Permissions":      {"Grant the bot the permission named above in Server Settings.", "Then run the command again."},
	"Insufficient Permissions": {"Give the bot **Send Messages** and **Embed Links** in that channel.", "Then run the command again."},
	"Slash Command Required":   {"Use the slash version of this command.", "Type `/` and pick it from the menu."},
	"Invalid Subcommand":       {"Choose one of the subcommands from the command's option list.", "Type `/` to browse the available options."},
	"Missing Option":           {"Fill in one of the command's options.", "Mention a player or type their username."},
	"Slow Down":                {"Wait a few seconds before trying again."},
	"Too Many Attempts":        {"Wait a few minutes, then try again."},
	"Not Yet Implemented":      {"This feature is still being built.", "Watch the guild's announcements for updates."},
	"Guild Not Registered":     {"An administrator must run `/setup init` to register the guild.", "Once it is registered, every command works."},
	"Command Error":            {"Wait a moment, then run the command again.", "If it keeps failing, tell a guild administrator what you ran and when."},
	"Configuration Error":      {"A guild administrator must finish the command's setup in the dashboard under Commands.", "Commands marked **Needs setup** list what is missing."},
	"Command Disabled":         {"A guild administrator turned this command off.", "They can enable it in the dashboard under Commands."},
	"Slash Disabled":           {"This command is switched off for slash usage.", "Try the prefix version, or ask a guild administrator to enable it."},
	"Prefix Disabled":          {"This command is switched off for prefix usage.", "Use the slash version, or ask a guild administrator to enable it."},
	"Slash Commands Disabled":  {"A guild administrator switched slash commands off.", "Try the prefix version of the command instead."},
	"Prefix Commands Disabled": {"A guild administrator switched prefix commands off.", "Use the slash version of the command instead."},
	"Subcommand Disabled":      {"A guild administrator turned this subcommand off.", "Try a different subcommand, or ask them to enable it in the dashboard."},
	"Channel Not Allowed":      {"Run the command in a channel where it is allowed.", "A guild administrator sets the allowed channels in the dashboard under Commands."},
	"Plan Inactive":            {"A guild administrator can request a plan or a renewal in the dashboard.", "Settings and setup commands keep working meanwhile."},
	"Guild Disabled":           {"Ranked Bedwars has been switched off for this guild.", "Contact support if you think this is a mistake."},
	"Unexpected Error":         {"Wait a moment, then run the command again.", "If it keeps failing, tell a guild administrator what you ran and when."},
}

// guidanceFallback is the closing block of an error that has no entry of its own.
var guidanceFallback = []string{"Check the details above and try again.", "If it keeps happening, ask staff to look into it."}

// withGuidance adds the closing "How to Fix" (errors) or "Next Step" (everything else) block to a built-in answer that
// has no fields of its own. Embeds without an entry only get the block when they are errors.
func withGuidance(embed *discordgo.MessageEmbed, presetType, title string) {
	if embed == nil || len(embed.Fields) > 0 || title == "" || presetType == "transparent" || presetType == "loading" {
		return
	}
	lines, ok := answerGuidance[title]
	if !ok {
		if presetType != "error" {
			return
		}
		lines = guidanceFallback
	}
	name := "Next Step"
	if presetType == "error" || presetType == "caution" {
		name = "How to Fix"
	}
	quoted := make([]string, len(lines))
	for n, line := range lines {
		quoted[n] = "> " + line
	}
	embed.Fields = []*discordgo.MessageEmbedField{{Name: name, Value: strings.Join(quoted, "\n")}}
}

// ============================================================================
// Command Embeds
// Every answer a slash or prefix command gives lives here, grouped by the command file that sends it.
// ============================================================================

// ============================================================================
