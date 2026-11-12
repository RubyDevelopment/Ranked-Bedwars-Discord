package match

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/queue"
	"ranked-bedwars/src/types"
)

// Void votes: any player of a game may vote to void it, and the game is voided once three quarters of its players (rounded down, at least one)
// have voted to. /void opens the vote with Agree and Disagree buttons, so nobody has to type the command again; a player's vote
// counts once, and they may change it. The vote fails once too many players disagreed for it to reach the threshold.

var voidVotes = struct {
	mu sync.Mutex
	by map[uint]map[string]bool // game id -> user id -> true (agrees) or false (disagrees)
	at map[uint]time.Time       // game id -> when its vote was opened
}{by: map[uint]map[string]bool{}, at: map[uint]time.Time{}}

// voidVoteLife is how long a vote stays open without a ballot: it then fails by itself, and every ballot restarts the countdown.
const voidVoteLife = 5 * time.Minute

// voidVoteOpen reports whether the game has a void vote going.
func voidVoteOpen(gameID uint) bool {
	voidVotes.mu.Lock()
	defer voidVotes.mu.Unlock()
	return len(voidVotes.by[gameID]) > 0 && time.Since(voidVotes.at[gameID]) < voidVoteLife
}

// VoidVotesNeeded is how many votes void a game of this many players: three quarters, rounded down, and never fewer than one.
func VoidVotesNeeded(players int) int {
	return max(1, players*3/4)
}

// voidVoteCounts is how many players agree and disagree. The caller holds the lock.
func voidVoteCounts(gameID uint) (yes, no int) {
	for _, agrees := range voidVotes.by[gameID] {
		if agrees {
			yes++
		} else {
			no++
		}
	}
	return yes, no
}

func castVoidBallot(gameID uint, userID string, agrees bool, players int) (yes, no, needed int, already bool) {
	voidVotes.mu.Lock()
	defer voidVotes.mu.Unlock()
	if voidVotes.by[gameID] == nil {
		voidVotes.by[gameID] = map[string]bool{}
	}
	// Every ballot restarts the countdown; the timers of earlier ballots see a newer stamp and do nothing.
	touched := time.Now()
	voidVotes.at[gameID] = touched
	time.AfterFunc(voidVoteLife+time.Second, func() { expireVoidVote(gameID, touched) })
	previous, voted := voidVotes.by[gameID][userID]
	already = voted && previous == agrees
	voidVotes.by[gameID][userID] = agrees
	yes, no = voidVoteCounts(gameID)
	return yes, no, VoidVotesNeeded(players), already
}

// CastVoidVote records a player's vote to void and returns the votes to void so far, the votes needed, and whether this
// player had already voted that way.
func CastVoidVote(gameID uint, userID string, players int) (votes, needed int, already bool) {
	votes, _, needed, already = castVoidBallot(gameID, userID, true, players)
	return votes, needed, already
}

// VoidVoters lists who agreed and who disagreed, in a stable order.
func VoidVoters(gameID uint) (yes, no []string) {
	voidVotes.mu.Lock()
	defer voidVotes.mu.Unlock()
	for id, agrees := range voidVotes.by[gameID] {
		if agrees {
			yes = append(yes, id)
		} else {
			no = append(no, id)
		}
	}
	sort.Strings(yes)
	sort.Strings(no)
	return yes, no
}

// ClearVoidVotes forgets the votes of a game (it ended or was voided).
func ClearVoidVotes(gameID uint) {
	voidVotes.mu.Lock()
	delete(voidVotes.by, gameID)
	delete(voidVotes.at, gameID)
	voidVotes.mu.Unlock()
}

// voidVotePrefix identifies the vote buttons: voidvote|yes|<game id> or voidvote|no|<game id>.
const voidVotePrefix = "voidvote|"

// VoidVoteButtons are the Agree and Disagree buttons under a void vote.
func VoidVoteButtons(gameID uint) []discordgo.MessageComponent {
	id := strconv.FormatUint(uint64(gameID), 10)
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: "Agree", Style: discordgo.SuccessButton, CustomID: voidVotePrefix + "yes|" + id},
		&discordgo.Button{Label: "Disagree", Style: discordgo.DangerButton, CustomID: voidVotePrefix + "no|" + id},
	}}}
}

func mentionList(ids []string) string {
	if len(ids) == 0 {
		return "—"
	}
	out := make([]string, len(ids))
	for n, id := range ids {
		out[n] = "<@" + id + ">"
	}
	return strings.Join(out, " ")
}

// VoidVotePanel is the open vote: how many agree out of how many are needed, and who voted which way.
func VoidVotePanel(game Game) *discordgo.MessageEmbed {
	yes, no := VoidVoters(game.ID)
	needed := VoidVotesNeeded(len(game.Players))
	return helpers.Preset("info", types.PresetOptions{
		Title: fmt.Sprintf("Void Vote · Game #%d", game.Number),
		Description: fmt.Sprintf("Press a button to vote. The game is voided when **%d** of **%d** players agree.\n\n**Agree (%d/%d):** %s\n**Disagree (%d):** %s",
			needed, len(game.Players), len(yes), needed, mentionList(yes), len(no), mentionList(no)),
		Thumbnail: helpers.ThumbTrashCan,
	})
}

func voidVoteClosedEmbed(title, text string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbTrashCan})
}

func init() { helpers.RegisterComponentHandler(handleVoidVoteClick) }

// handleVoidVoteClick counts a press of Agree or Disagree and updates the vote message: the new tally, or the result.
func handleVoidVoteClick(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, voidVotePrefix) {
		return false
	}
	parts := strings.Split(customID, "|")
	if len(parts) != 3 {
		return false
	}
	raw, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return false
	}
	gameID := uint(raw)
	update := func(embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: components},
		})
	}
	say := func(text string) {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Content: text},
		})
	}

	game, ok := Games.Get(gameID)
	if !ok {
		update(voidVoteClosedEmbed("Void Vote Closed", "This game is already over."), []discordgo.MessageComponent{})
		return true
	}
	actor := helpers.InteractionUserID(i)
	if _, isPlayer := game.Player(actor); !isPlayer {
		say("Only the players of this game can vote.")
		return true
	}

	players := len(game.Players)
	yes, no, needed, _ := castVoidBallot(gameID, actor, parts[1] == "yes", players)

	switch {
	case yes >= needed:
		reason := fmt.Sprintf("Void vote passed (%d of %d players)", yes, players)
		if err := Games.Void(actor, i.GuildID, gameID, reason, VoidOptions{}); err != nil {
			say("The game could not be voided: " + err.Error())
			return true
		}
		update(voidVoteClosedEmbed("Void Vote Passed", fmt.Sprintf("Game #%d was voided (%d of %d players agreed).", game.Number, yes, players)), []discordgo.MessageComponent{})
	case players-no < needed: // even if everyone left agreed, it could not pass
		ClearVoidVotes(gameID)
		returnToTeamVoice(s, game) // players drift into the queue channels during a vote: bring them back
		update(voidVoteClosedEmbed("Void Vote Failed", fmt.Sprintf("Too many players disagreed, so Game #%d keeps going.", game.Number)), []discordgo.MessageComponent{})
	default:
		update(VoidVotePanel(game), VoidVoteButtons(gameID))
	}
	return true
}

// expireVoidVote treats a vote nobody settled in voidVoteLife as failed: the game goes on, and anyone waiting in a queue channel
// is sent back to their team voice channel. A vote that ended meanwhile (or was reopened later) is left alone.
func expireVoidVote(gameID uint, opened time.Time) {
	voidVotes.mu.Lock()
	still := voidVotes.at[gameID].Equal(opened)
	voidVotes.mu.Unlock()
	if !still {
		return
	}
	ClearVoidVotes(gameID)
	if s, game := base.Session.Load(), (Game{}); s != nil {
		var ok bool
		if game, ok = Games.Get(gameID); ok {
			returnToTeamVoice(s, game)
		}
	}
}

// returnToTeamVoice moves the players who are sitting in a queue channel back to their team's voice channel (after a failed void vote).
// Anyone in another channel, or not in voice, is left where they are.
func returnToTeamVoice(s *discordgo.Session, g Game) {
	for _, p := range g.Players {
		vcID := g.Channels.Voice[p.Team]
		if vcID == "" {
			continue
		}
		vs, err := s.State.VoiceState(g.GuildID, p.UserID)
		if err != nil || vs == nil || vs.ChannelID == "" || vs.ChannelID == vcID || !queue.IsQueueChannel(g.GuildID, vs.ChannelID) {
			continue
		}
		_ = s.GuildMemberMove(g.GuildID, p.UserID, &vcID)
	}
}
