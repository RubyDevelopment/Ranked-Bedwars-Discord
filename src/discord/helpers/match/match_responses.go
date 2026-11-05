package match

import (
	"errors"
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

func thumb(t embedTemplate, url string) embedTemplate { t.Thumbnail = &url; return t }

// plainText makes the template a message of just this text (no embed box), the picture attached below it.
func plainText(t embedTemplate, text string) embedTemplate {
	box := false
	t.Content, t.Box = &text, &box
	return t
}

func colored(t embedTemplate, hex string) embedTemplate { t.Color = &hex; return t }

// GameActionErrorEmbed maps a GameManager error to the embed for it. action completes
// "trying to ..." in the fallback embed (e.g. "void the game").
func GameActionErrorEmbed(action string, err error) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, ErrGameNotFound):
		return gameErrorEmbed("Game Not Found", "That game could not be found. It may have already been cleaned up.")
	case errors.Is(err, ErrWrongState):
		return gameErrorEmbed("Invalid Game State", "That game is not in the right state for this action.")
	case errors.Is(err, ErrNotParticipant):
		return gameErrorEmbed("Not in Match", "That player is not a participant in this game.")
	case errors.Is(err, ErrAlreadySubmitted):
		return gameErrorEmbed("Already Submitted", "A result has already been submitted for this game.")
	case errors.Is(err, ErrUnknownTeam):
		return gameErrorEmbed("Unknown Team", "That team does not exist in this game.")
	case errors.Is(err, ErrNoSeason):
		return gameErrorEmbed("No Active Season", "This guild does not have an active season configured. Please contact an administrator to configure a season.")
	case errors.Is(err, ErrNothingToRestore):
		return gameErrorEmbed("Nothing to Restore", "That game has no recorded result to restore.")
	case errors.Is(err, ErrBusy):
		return gameErrorEmbed("Game Busy", "That game is currently being scored. Please try again in a moment.")
	case errors.Is(err, ErrAlreadyVoided):
		return gameErrorEmbed("Already Voided", "That game has already been voided.")
	case errors.Is(err, ErrNotScored):
		return gameErrorEmbed("Not Scored", "That game has not been scored yet.")
	case errors.Is(err, ErrRescoreIncomplete):
		return gameErrorEmbed("Rescore Incomplete", "The old results were reversed but the new ones could not be applied. Run the score again to finish.")
	case errors.Is(err, ErrNotYourTurn):
		return gameErrorEmbed("Not Your Turn", "It is not your turn to pick.")
	case errors.Is(err, ErrNotInPool):
		return gameErrorEmbed("Invalid Selection", "That player is not available to pick.")
	case errors.Is(err, ErrDraftOver):
		return gameErrorEmbed("Draft Complete", "The captain draft is already complete.")
	case errors.Is(err, ErrNoDraftYet):
		return gameErrorEmbed("No Active Draft", "There is no active captain draft for this game.")
	case errors.Is(err, ErrNotACaptain):
		return gameErrorEmbed("Not a Captain", "Only team captains can pick players during the draft.")
	}
	return helpers.UnexpectedErrorGameEmbed(action)
}

func gameErrorEmbed(title, description string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       title,
		Description: description,
	})
}

func HostGameErrorEmbed(err error) *discordgo.MessageEmbed {
	var busy *BusyError
	var unregistered *NotRegisteredError
	text := err.Error()
	switch {
	case errors.Is(err, ErrGameNotFound):
		text = "That game isn't running any more."
	case errors.Is(err, ErrNotCustom):
		text = "That game wasn't built with `/host`."
	case errors.Is(err, ErrBotHosted):
		text = "A bot is hosting that game, so its roster can't be changed."
	case errors.Is(err, ErrUnknownTeam):
		text = "That game doesn't have that team."
	case errors.Is(err, ErrNotParticipant):
		text = "That player isn't in the game."
	case errors.Is(err, ErrWrongState):
		text = "The game isn't in a state where that can be done."
	case errors.As(err, &busy):
		text = fmt.Sprintf("<@%s> is already in a game.", busy.UserID)
	case errors.As(err, &unregistered):
		text = "That player isn't registered."
	}
	return helpers.Preset("error", types.PresetOptions{Title: "Hosted Game", Description: text})
}

// statusFields is how the old party embeds listed who was not invited yet, who was invited and who had joined.
var statusFields = []embedField{
	{Name: helpers.DotRed + " Waiting ({waitingCount})", Value: "{waiting}", Inline: true},
	{Name: helpers.DotYellow + " Invited ({invitedCount})", Value: "{invited}", Inline: true},
	{Name: helpers.DotGreen + " Joined ({joinedCount}/{playerCount})", Value: "{joined}", Inline: true},
}

var teamStatusField = embedField{Name: "Team {team}", Value: "{teamPlayers}", Inline: true, Each: "team"}

// mapScoreField is one field per map of the vote, best first: the map and its score, the winner marked.
var mapScoreField = embedField{Name: "{mapIcon} {mapName}{winnerTag}", Value: "`Score: {mapScore}`", Inline: true, Each: "map"}

var submitYourWin = thumb(tpl("info", "Submit Your Win",
	"When your match ends, send a **screenshot of the win screen** in this channel.\n\nYou can submit **3 minutes** after the match starts.\n\nRun **`/submit`** and attach the image, or send **`=submit`** with the screenshot attached to the same message."), helpers.ThumbSprout)

// The defaults are the old bot's embeds, word for word. Embeds the old bot never had are switched off.
var defaultEmbeds = map[string]embedTemplate{
	// Pre-game
	"introduction": func() embedTemplate {
		t := thumb(tpl("success", "Welcome to Game #{game}", "Welcome to **{flow} {mode}** {teamClause}. Defend your bed, destroy enemy beds, and fight until one team remains. Your team voice channel is ready, join it to coordinate with your team!", teamField), helpers.ThumbDoubleSwords)
		t.Content = str("{mentions}")
		return t
	}(),
	"teams":            thumb(tpl("success", "Drafting Complete", "Captain drafting is finished. Team voice channels are being prepared.", teamField), helpers.ThumbTada),
	"botConnecting":    off(tpl("pending", "Host Joining", "`{bot}` is joining the server to host your match.")),
	"privateGameReady": off(tpl("info", "Private Game Ready", "`{bot}` opened the private game. Party invites are next.")),
	"partyInvitation": thumb(tpl("pending", "Party Invitation",
		"The host is sending party invites now. Accept the in-game invite to join.\n\n**Not getting an invite?**\n1. Make sure your status is **Online**.\n2. Make sure **Party Invites** are **Enabled**.\n3. Make sure you are using the correct username.\n\nYou can also join manually with `/party join {bot}`.",
		append([]embedField{
			{Name: "Host", Value: "`{bot}`", Inline: true},
			{Name: "Ending", Value: "<t:{endsAt}:R>", Inline: true},
			{Name: "​", Value: "​", Inline: true},
		}, statusFields...)...), helpers.ThumbTimer),
	// The map vote and the captain draft: interactive, so they are always posted, but their wording is the guild's.
	"mapVoting":    thumb(tpl("pending", "Map Voting", "Game {gameLink} ({mode}) needs a map. Pick the one you want to play with the buttons below. There are **{mapCount} maps** to choose from and you can change your vote until the timer runs out.\n\n**Voting ends:** <t:{endsAt}:R>\n**Votes cast:** {voted} of {playerCount} players\n**Result:** the map with the most votes is played.", mapScoreField), helpers.ThumbTimer),
	"mapVoteEnded": thumb(tpl("success", "Map Voting Ended", "The majority of votes went to **{map}**! Please wait while the map is being selected...", mapScoreField), helpers.ThumbTada),
	"noMaps":       thumb(tpl("caution", "Map Setup", "No maps are enabled for this server right now. Contact staff if you think this is a mistake."), helpers.ThumbFlag),
	"captainPicking": func() embedTemplate {
		t := tpl("pending", "Captain Picking", "It is {captain}'s turn to pick for **{team}**. Choose a player from the menu below, or use `=pick`.\n\n**Time left:** {timeLeft}",
			embedField{Name: "{team}", Value: "{teamPlayers}", Inline: true, Each: "team"}, embedField{Name: "Remaining Players", Value: "{remaining}"})
		t.Thumbnail = str("{captainAvatar}")
		return t
	}(),
	"partyMembersAutoAssigned": thumb(tpl("info", "Party Members Joined", "{names} {wasWere} placed on **{team}** to stay with {captain}."), helpers.ThumbPlayers),
	"matchJoined": thumb(tpl("pending", "Match Joined", "The bot joined the private game lobby and is waiting for the Team Selector to open. Teams will be checked once it does.",
		embedField{Name: "Team TBD", Value: "{teamPlayers}", Inline: true, Each: "team"}), helpers.ThumbTimer),
	// Hosted by hand: nobody joins the server for the players, so they are told how to set the game up.
	"partySetup": thumb(tpl("pending", "Party Setup", "It is time to get everyone into a Minecraft party. **Decide who will host**, then invite the rest of the lobby.\n\nBefore inviting, make sure that:\n1. You are **on the server**.\n2. **Party invites** are set to **On**.\n3. Your status is set to **Online**.",
		embedField{Name: "Party Command", Value: "```{inviteCommand}```"}, teamField), helpers.ThumbPlayers),
	"mapSetup": thumb(tpl("pending", "Map Setup", "**{mapCount}** map(s) are in rotation for **{mode}**.\n\n**Agree on one map**, create a private game on it, then use its max height and team colours below to set up your match.",
		embedField{Name: "{mapName}", Value: "**Max height:** {mapHeight}\n**Teams:** {mapTeams}", Inline: true, Each: "map"}), helpers.ThumbSprout),
	"teamSetup": thumb(tpl("pending", "Team Setup", "Open the **Team Selector** item in the lobby and join your team's colour. **Stay with your team mates**, and do not start the countdown until every player is on their team.",
		embedField{Name: "Team {team}", Value: "{teamPlayers}", Inline: true, Each: "team"}), helpers.ThumbPlayers),
	"partyReminder": func() embedTemplate {
		t := tpl("default", "Party reminder", "Said again each time the players who have not joined are invited.")
		t.Content = str("{waitingMentions}\nMake sure you're **/online**, party invites are **on**, you are in the **hub or Bedwars lobby**, and you are on the right **account**!")
		return t
	}(),
	"partyReady": thumb(tpl("success", "Party Ready", "Everyone has joined the party. The match is about to begin, and the party will be warped to **{mode}** shortly.\n\nDo not leave the party.",
		append([]embedField{{Name: "Host", Value: "`{bot}`"}}, statusFields...)...), helpers.ThumbTada),
	"partyTimeout": thumb(tpl("error", "Party Timeout", "The game was **voided** because not everyone joined the party in time.\n\nPlayers who did not join were penalized automatically.",
		statusFields...), helpers.ThumbTrashCan),
	"noBot":             off(tpl("caution", "No Host Available", "No hosting bot is free right now.\n\n{reason}")),
	"botConnectFailed":  off(thumb(tpl("error", "Host Could Not Join", "`{bot}` could not join the server.\n\n> **What happened:** {reason}"), helpers.ThumbFlag)),
	"privateGameFailed": off(thumb(tpl("error", "Private Game Failed", "The private game could not be opened.\n\n> **What happened:** {reason}"), helpers.ThumbFlag)),
	"mapFailed":         off(thumb(tpl("error", "Map Selection Failed", "The map **{map}** could not be selected.\n\n> **What happened:** {reason}"), helpers.ThumbFlag)),
	"settingsFailed":    off(thumb(tpl("error", "Game Settings Failed", "The game settings could not be applied.\n\n> **What happened:** {reason}"), helpers.ThumbFlag)),
	"startFailed":       off(thumb(tpl("error", "Match Did Not Start", "The match could not be started.\n\n> **What happened:** {reason}"), helpers.ThumbFlag)),
	// Not in the old bot, but only posted when a step is stuck: it tells whoever set the server up what to fix.
	"hostProblem": thumb(tpl("error", "Hosting Problem", "The hosting bot got stuck on **{step}**.\n\n> **What happened:** {reason}\n\n{hint}",
		embedField{Name: "What the bot last saw", Value: "```{diagnosis}```"},
		embedField{Name: "For whoever set this server up", Value: "Lines starting with ⚠ had no rule that reacted to them. Fix it in the dashboard under Game Server → Minecraft Server → Server automations (Rules and Commands), then host again."}), helpers.ThumbFlag),
	"mapSelected": thumb(tpl("success", "Map Voting Ended", "The map selection has concluded! The winning map is **{map}**. Please wait while the game prepares the lobby.", mapScoreField), helpers.ThumbTada),
	"teamSelection": thumb(tpl("pending", "Team Selection",
		"The bot is checking that every player is on their assigned team.\n\n**What to do:**\n1. Open the **Team Selector** item.\n2. Join your assigned **Team Color**.\n\n**Ends:** <t:{endsAt}:R>",
		teamStatusField), helpers.ThumbTimer),
	"teamsVerified": thumb(tpl("success", "Teams Verified", "Every player is on their assigned team. The game is ready to start.", teamStatusField), helpers.ThumbTada),
	"teamTimeout":   thumb(tpl("error", "Team Selection Timeout", "Not every player was on their assigned team in time. Check that each player is on the correct team before starting."), helpers.ThumbFlag),

	// In-game
	"matchStarted":       thumb(tpl("success", "Match Started", "The game has started and the Team Selector is closed. The rosters below are the teams for this match.", teamStatusField), helpers.ThumbTada),
	"matchStartedManual": submitYourWin,
	"liveTracker": tpl("info", "Game #{game} Live Match Tracker",
		"Here is a real-time team rosters and elimination status for the `{mode}` match!\n\n**Time Elapsed:** <t:{startedAt}:R>",
		embedField{Name: "{team} {bed}", Value: "{teamPlayers}", Inline: true, Each: "team"}),
	"kill":           off(tpl("info", "Kill", "{killer} killed {victim}.")),
	"firstBlood":     off(tpl("caution", "First Blood", "{killer} drew first blood on {victim}.")),
	"bedDestroyed":   off(tpl("caution", "Bed Destroyed", "**{team}**'s bed was destroyed by {destroyer}.")),
	"finalKill":      off(tpl("info", "Final Kill", "{killer} eliminated {victim}.")),
	"teamEliminated": off(tpl("caution", "Team Eliminated", "**{team}** has been eliminated.")),
	"phaseChanged":   off(tpl("info", "{phaseName}", "The match moved on to phase {phase}.")),
	"statTracked":    off(tpl("info", "{stat}", "{player} +{amount} ({total} total)")),

	// Post-game
	"matchEnded": off(tpl("info", "Game #{game} • Match Over", "**{winner}** won the match. The result is being scored.",
		embedField{Name: "{team}", Value: "{teamStats}", Inline: false, Each: "team"})),
	"scored":     plainText(tpl("default", "Match Finished", ""), "**{gameLink}**\n**Winners:** {winnerMentions}\n**Losers:** {loserMentions}"), // a plain message with the results card attached, not a box
	"casualDone": thumb(colored(tpl("success", "Casual Match Logged • Game #{game}", "**Result:** Tied · no team won.\n\nThe match is complete and this game room is closing."), "#57f287"), helpers.ThumbTada),
	"staffPending": thumb(colored(tpl("pending", "Win Submitted • Game #{game}",
		"**Status:** Pending staff scoring\n**Claimed winner:** {winner}\n\nThe match was moved to the review category. Staff will score it shortly."), "#fee75c"), helpers.ThumbTimer),
	"voided": thumb(tpl("error", "Game Voided", "This game was voided and will not count towards your stats.\n\n**Reason:** {reason}"), helpers.ThumbFlag),
}
