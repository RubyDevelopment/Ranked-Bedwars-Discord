package helpers

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
)

// A strike request is a thread opened from the request message: the request itself (who, why, who played) and a vote that the
// players of that game answer with Agree and Disagree buttons. Three quarters of them agreeing gives the strike.
//
// ponytail: the open votes live in memory, so a bot restart closes them (a press then says so); store them in the database if
// restarts during a vote turn out to matter.

type strikePlayer struct {
	UserID string
	Team   string
	Winner bool
}

type strikeRequest struct {
	mu        sync.Mutex
	guildID   string
	game      int
	suspectID string
	reporter  string
	reason    string
	voters    []string        // the game's players except the suspect
	votes     map[string]bool // voter -> agrees
	closed    bool
}

var strikeRequests = struct {
	mu sync.Mutex
	by map[string]*strikeRequest // thread id -> the open request
}{by: map[string]*strikeRequest{}}

// strikeVotesNeeded is three quarters of the voters, rounded up (at least one).
func strikeVotesNeeded(voters int) int { return max(1, (voters*3+3)/4) }

func (r *strikeRequest) counts() (agree, disagree int) {
	for _, agrees := range r.votes {
		if agrees {
			agree++
		} else {
			disagree++
		}
	}
	return agree, disagree
}

// strikeGame finds the game a strike request is about and who played in it: the game with that number the suspect was in, or
// (no number given) the suspect's latest game.
func (m *GameManager) strikeGame(guildID, suspectID string, number int) (gameNumber int, players []strikePlayer, err error) {
	svc := database.Get()
	if svc == nil {
		return 0, nil, errors.New("database not ready")
	}
	view, err := svc.Player.Get(guildID, suspectID)
	if err != nil {
		return 0, nil, err
	}
	history, err := svc.Player.AllGames(guildID, sdk.UintToString(view.Player.ID))
	if err != nil {
		return 0, nil, err
	}
	var chosen *sdk.GameHistory
	for n := range history {
		g := history[n].Game
		if g == nil {
			continue
		}
		if number > 0 {
			if g.GameNumber == number {
				chosen = &history[n]
				break
			}
			continue
		}
		if chosen == nil || gameWhen(*g).After(gameWhen(*chosen.Game)) {
			chosen = &history[n]
		}
	}
	if chosen == nil {
		return 0, nil, errNoStrikeGame
	}
	rows, err := m.api().Participants(guildID, chosen.Game.ID)
	if err != nil {
		return 0, nil, err
	}
	players = make([]strikePlayer, len(rows))
	var wg sync.WaitGroup
	for n, row := range rows {
		players[n] = strikePlayer{Team: row.Team, Winner: row.IsWinner}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if global, err := svc.Player.GetGlobal(sdk.UintToString(row.PlayerID)); err == nil {
				players[n].UserID = global.SnowflakeID
			}
		}()
	}
	wg.Wait()
	players = append(players[:0:0], filterStrikePlayers(players)...)
	return chosen.Game.GameNumber, players, nil
}

var errNoStrikeGame = errors.New("no game found")

func filterStrikePlayers(in []strikePlayer) []strikePlayer {
	out := in[:0:0]
	for _, p := range in {
		if p.UserID != "" {
			out = append(out, p)
		}
	}
	return out
}

func mentionIDs(ids []string) string {
	if len(ids) == 0 {
		return "—"
	}
	parts := make([]string, len(ids))
	for n, id := range ids {
		parts[n] = "<@" + id + ">"
	}
	return strings.Join(parts, ", ")
}

// StrikeRequestGameEmbed is the error shown when the game of a request cannot be found.
func strikeRequestNoGameEmbed(number int) *discordgo.MessageEmbed {
	text := "That player has no game on record to review."
	if number > 0 {
		text = fmt.Sprintf("That player was not in a game numbered **#%d**.", number)
	}
	return Preset("error", types.PresetOptions{Title: "Game Not Found", Description: text, Thumbnail: ThumbFlag})
}

// OpenStrikeRequest opens the thread of a strike request from the request message and posts the request and its vote in it.
// It returns the thread, or an embed that says why there is none. number is the game to review (0 = the suspect's latest).
func OpenStrikeRequest(s *discordgo.Session, i *discordgo.InteractionCreate, suspect *discordgo.User, username, reason string, number int) (*discordgo.Channel, *discordgo.MessageEmbed) {
	if ch, err := s.State.Channel(i.ChannelID); err == nil && ch.IsThread() {
		return nil, SimpleErrorEmbed("A strike request opens its own thread, so it cannot be made inside a thread. Use it in a text channel.")
	}
	gameNumber, players, err := Games.strikeGame(i.GuildID, suspect.ID, number)
	if err != nil {
		if errors.Is(err, errNoStrikeGame) {
			return nil, strikeRequestNoGameEmbed(number)
		}
		return nil, UnexpectedErrorModerationStrikeEmbed(i)
	}
	reporter := InteractionUserID(i)

	voters := make([]string, 0, len(players))
	ping := []string{suspect.ID, reporter}
	for _, p := range players {
		if p.UserID != suspect.ID {
			voters = append(voters, p.UserID)
		}
		ping = append(ping, p.UserID)
	}
	ping = uniqueStrings(ping)

	// The thread hangs off the player's own message; a slash request (a private answer) posts a public line first.
	messageID := PrefixSourceMessageID(i)
	if messageID == "" {
		msg, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Content:         "<@" + reporter + "> requested a strike review for <@" + suspect.ID + ">.",
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		})
		if err != nil {
			return nil, SimpleErrorEmbed("The strike request could not be posted here. Check that the bot can send messages and create threads in this channel.")
		}
		messageID = msg.ID
	}
	thread, err := s.MessageThreadStartComplex(i.ChannelID, messageID, &discordgo.ThreadStart{
		Name:                "Strike Request for " + username,
		AutoArchiveDuration: 1440,
	})
	if err != nil {
		return nil, SimpleErrorEmbed("The thread for the strike request could not be created. Check that the bot can create threads in this channel.")
	}

	req := &strikeRequest{guildID: i.GuildID, game: gameNumber, suspectID: suspect.ID, reporter: reporter, reason: reason, voters: voters, votes: map[string]bool{}}
	strikeRequests.mu.Lock()
	strikeRequests.by[thread.ID] = req
	strikeRequests.mu.Unlock()

	var winners, losers []string
	for _, p := range players {
		line := fmt.Sprintf("• <@%s> [%s]", p.UserID, p.Team)
		if p.Winner {
			winners = append(winners, line)
		} else {
			losers = append(losers, line)
		}
	}
	listOrDash := func(lines []string) string {
		if len(lines) == 0 {
			return "—"
		}
		return strings.Join(lines, "\n")
	}
	request := Preset("default", types.PresetOptions{
		Title:       fmt.Sprintf("Strike Request • Game #%d", gameNumber),
		Description: "A strike review request was submitted. Use this thread to discuss it and reach a decision.",
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Suspect", Value: "<@" + suspect.ID + ">", Inline: true},
			{Name: "Reporter", Value: "<@" + reporter + ">", Inline: true},
			{Name: "Reason", Value: reason},
			{Name: "Winners", Value: listOrDash(winners), Inline: true},
			{Name: "Losers", Value: listOrDash(losers), Inline: true},
		},
		Thumbnail: MinecraftBustAvatarURL(username),
	})
	WithGuildFooterIconForGuild(s, i.GuildID, request)
	_, _ = s.ChannelMessageSendComplex(thread.ID, &discordgo.MessageSend{
		Content:         mentionsLine(ping),
		Embeds:          []*discordgo.MessageEmbed{request},
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: ping},
	})
	req.mu.Lock()
	vote := strikeVoteEmbed(req)
	req.mu.Unlock()
	WithGuildFooterIconForGuild(s, i.GuildID, vote)
	_, _ = s.ChannelMessageSendComplex(thread.ID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{vote}, Components: strikeVoteButtons(thread.ID, req)})
	return thread, nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func mentionsLine(ids []string) string {
	parts := make([]string, len(ids))
	for n, id := range ids {
		parts[n] = "<@" + id + ">"
	}
	return strings.Join(parts, " ")
}

// strikeVoteEmbed is the open vote (the caller holds req.mu).
func strikeVoteEmbed(req *strikeRequest) *discordgo.MessageEmbed {
	agree, disagree := req.counts()
	needed := strikeVotesNeeded(len(req.voters))
	return Preset("caution", types.PresetOptions{
		Title: fmt.Sprintf("Strike Vote for Game #%d", req.game),
		Description: fmt.Sprintf("A strike request was submitted against <@%s>. Only players from Game #%d can vote, and **%d/%d** agree votes (3/4) are needed to apply the strike.\n\n**Reason:** %s\n**Voters:** %s\n\n**Current votes**\n🟢 Agree: **%d**\n🔴 Disagree: **%d**",
			req.suspectID, req.game, needed, len(req.voters), req.reason, mentionIDs(req.voters), agree, disagree),
	})
}

const strikeVotePrefix = "srvote|"

func strikeVoteButtons(threadID string, req *strikeRequest) []discordgo.MessageComponent {
	agree, disagree := req.counts()
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: "Agree (" + strconv.Itoa(agree) + ")", Style: discordgo.SuccessButton, CustomID: strikeVotePrefix + "yes|" + threadID},
		&discordgo.Button{Label: "Disagree (" + strconv.Itoa(disagree) + ")", Style: discordgo.DangerButton, CustomID: strikeVotePrefix + "no|" + threadID},
	}}}
}

func init() { RegisterComponentHandler(handleStrikeVote) }

// handleStrikeVote counts an Agree or Disagree press: it updates the vote, and ends it with the strike when enough agree or
// with no strike when too many disagree for it to pass.
func handleStrikeVote(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, strikeVotePrefix) {
		return false
	}
	parts := strings.Split(customID, "|")
	if len(parts) != 3 {
		return false
	}
	threadID := parts[2]
	update := func(embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) {
		_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{Embeds: embeds, Components: components},
		})
	}
	say := func(text string) {
		_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Content: text},
		})
	}

	strikeRequests.mu.Lock()
	req := strikeRequests.by[threadID]
	strikeRequests.mu.Unlock()
	if req == nil {
		update([]*discordgo.MessageEmbed{Preset("info", types.PresetOptions{Title: "Vote Closed", Description: "This vote is no longer open (it ended, or the bot restarted). Ask staff to review the request."})}, []discordgo.MessageComponent{})
		return true
	}

	voter := InteractionUserID(i)
	req.mu.Lock()
	defer req.mu.Unlock()
	if req.closed {
		say("This vote is already over.")
		return true
	}
	if !slices.Contains(req.voters, voter) {
		say("Only the other players of Game #" + strconv.Itoa(req.game) + " can vote on this request.")
		return true
	}
	req.votes[voter] = parts[1] == "yes"
	agree, disagree := req.counts()
	needed := strikeVotesNeeded(len(req.voters))

	switch {
	case agree >= needed:
		req.closed = true
		suspect := &discordgo.User{ID: req.suspectID}
		if u, err := s.User(req.suspectID); err == nil {
			suspect = u
		}
		result, ok := IssueStrike(s, req.guildID, actorSystem, suspect, fmt.Sprintf("%s (strike vote, game #%d)", req.reason, req.game), "")
		title, text := "Strike Vote Passed", fmt.Sprintf("**%d/%d** players agreed, so the strike was applied.", agree, len(req.voters))
		if !ok {
			title, text = "Strike Vote Passed", fmt.Sprintf("**%d/%d** players agreed, but the strike could not be applied. Staff must issue it with `/strike`.", agree, len(req.voters))
		}
		final := Preset("success", types.PresetOptions{Title: title, Description: text})
		update([]*discordgo.MessageEmbed{final, result}, []discordgo.MessageComponent{})
		strikeRequests.mu.Lock()
		delete(strikeRequests.by, threadID)
		strikeRequests.mu.Unlock()
	case len(req.voters)-disagree < needed: // even if everyone left agreed it could not pass
		req.closed = true
		update([]*discordgo.MessageEmbed{Preset("error", types.PresetOptions{
			Title:       "Strike Vote Failed",
			Description: fmt.Sprintf("Too many players disagreed (**%d**), so no strike was given. Staff can still review the request here.", disagree),
		})}, []discordgo.MessageComponent{})
		strikeRequests.mu.Lock()
		delete(strikeRequests.by, threadID)
		strikeRequests.mu.Unlock()
	default:
		update([]*discordgo.MessageEmbed{strikeVoteEmbed(req)}, strikeVoteButtons(threadID, req))
	}
	return true
}

