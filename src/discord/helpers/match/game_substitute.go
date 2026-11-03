package match

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/types"
)

// ============================================================================

// Substitute votes: while a bot-hosted game is inviting its players to the party, the engine counts how many rounds of invites each
// missing player has been through. When one is still missing after several (and has not said a word in the game chat), the game's other
// players are asked whether to swap them for a random player waiting in the queue the game came from. A majority of the others decides.
// The substitute takes the same team and place, moves into that team's voice channel, and the player who never came goes back to the
// waiting voice channel.

const (
	substituteVoteDuration = 60 * time.Second
	// A vote that did not pass is not opened again for the same player for this long.
	substituteCooldown = 5 * time.Minute
	substitutePrefix   = "subvote|"
)

// ErrNoSubstitute means nobody waiting in the queue could take the place.
var ErrNoSubstitute = errors.New("nobody is waiting in the queue to take the place")

type substituteVote struct {
	gameID  uint
	out     string
	voters  []string        // the game's other players
	ballots map[string]bool // user id -> agrees
	channel string
	message string
	stop    func() bool
}

var substituteVotes = struct {
	mu       sync.Mutex
	open     map[uint]*substituteVote // one per game
	cooldown map[string]time.Time     // "game:user" -> not before
}{open: map[uint]*substituteVote{}, cooldown: map[string]time.Time{}}

// substituteVotesNeeded is a majority of those who may vote.
func substituteVotesNeeded(voters int) int { return voters/2 + 1 }

func (v *substituteVote) counts() (yes, no []string) {
	for _, id := range v.voters {
		if agrees, voted := v.ballots[id]; voted {
			if agrees {
				yes = append(yes, id)
			} else {
				no = append(no, id)
			}
		}
	}
	return yes, no
}

func substituteKey(gameID uint, userID string) string { return strconv.FormatUint(uint64(gameID), 10) + ":" + userID }

func substituteButtons(gameID uint, out string) []discordgo.MessageComponent {
	id := strconv.FormatUint(uint64(gameID), 10)
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: "Agree", Style: discordgo.SuccessButton, CustomID: substitutePrefix + "yes|" + id + "|" + out},
		&discordgo.Button{Label: "Disagree", Style: discordgo.DangerButton, CustomID: substitutePrefix + "no|" + id + "|" + out},
	}}}
}

func substitutePanel(g Game, v *substituteVote, rounds int, offline bool) *discordgo.MessageEmbed {
	yes, no := v.counts()
	needed := substituteVotesNeeded(len(v.voters))
	why := fmt.Sprintf("<@%s> still has not joined the party after **%d** invites", v.out, rounds)
	if offline {
		why += ", is not online on Minecraft"
	}
	why += " and has not said anything in this chat."
	return helpers.Preset("info", types.PresetOptions{
		Title: fmt.Sprintf("Substitute Vote · Game #%d", g.Number),
		Description: fmt.Sprintf("%s\n\nSwap them for **a random player waiting in the queue**? They keep the same team, and <@%s> goes back to the waiting channel.\n\n"+
			"Press a button to vote. It passes at **%d** of **%d** votes and closes <t:%d:R>.\n\n**Agree (%d/%d):** %s\n**Disagree (%d):** %s",
			why, v.out, needed, len(v.voters), time.Now().Add(substituteVoteDuration).Unix(), len(yes), needed, mentionList(yes), len(no), mentionList(no)),
		Thumbnail: helpers.ThumbPlayers,
	})
}

func substituteClosedEmbed(title, text string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbPlayers})
}

// ============================================================================
// Opening a vote

// OfferSubstitute is the engine saying the player has still not joined the party after several rounds of invites. It opens a vote
// when the game is still being set up, the player has been silent in the game chat, someone is waiting in the queue to take the place,
// and no vote is open or was lost for this player a moment ago.
func (m *GameManager) OfferSubstitute(id uint, outUserID string, rounds int, offline bool) {
	g, ok := m.Get(id)
	if !ok || g.State != GamePreGame || g.Custom != nil || len(g.Teams) == 0 || g.Channels.Text == "" {
		return
	}
	out, ok := g.Player(outUserID)
	if !ok || out.Team == teamUnassigned {
		return
	}
	fx := m.fx()
	if fx.WaitingPlayers == nil || fx.HasSpoken == nil || fx.SwapMembers == nil {
		return
	}
	var voters []string
	for _, p := range g.Players {
		if p.UserID != outUserID {
			voters = append(voters, p.UserID)
		}
	}
	if len(voters) == 0 {
		return
	}

	key := substituteKey(id, outUserID)
	substituteVotes.mu.Lock()
	_, open := substituteVotes.open[id]
	cool := substituteVotes.cooldown[key]
	substituteVotes.mu.Unlock()
	if open || time.Now().Before(cool) {
		return
	}
	if fx.HasSpoken(g, outUserID) {
		return // they are talking: somebody who writes "getting on" is on their way, so no one gets voted out
	}
	if _, ok := m.pickSubstitute(g); !ok {
		return // nobody to bring in: a vote would have nothing to offer
	}
	s := base.Session.Load()
	if s == nil {
		return
	}

	v := &substituteVote{gameID: id, out: outUserID, voters: voters, ballots: map[string]bool{}, channel: g.Channels.Text}
	msg, err := s.ChannelMessageSendComplex(g.Channels.Text, &discordgo.MessageSend{
		Content:         mentionList(voters),
		Embeds:          []*discordgo.MessageEmbed{substitutePanel(g, v, rounds, offline)},
		Components:      substituteButtons(id, outUserID),
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: voters},
	})
	if err != nil {
		m.logf("open substitute vote for game %d: %v", id, err)
		return
	}
	v.message = msg.ID
	timer := time.AfterFunc(substituteVoteDuration, func() { m.closeSubstituteVote(id, "Substitute Vote Ended", "Not enough players voted to swap them, so nothing changes.", true) })
	v.stop = timer.Stop

	substituteVotes.mu.Lock()
	substituteVotes.open[id] = v
	substituteVotes.mu.Unlock()
}

// CancelSubstituteVote closes a vote about a player who joined the party after all.
func (m *GameManager) CancelSubstituteVote(id uint, userID string) {
	substituteVotes.mu.Lock()
	v, ok := substituteVotes.open[id]
	substituteVotes.mu.Unlock()
	if ok && v.out == userID {
		m.closeSubstituteVote(id, "Substitute Vote Cancelled", fmt.Sprintf("<@%s> joined the party, so nobody needs to be replaced.", userID), false)
	}
}

// forgetSubstituteVote drops the game's open vote and stops its timer; a vote that did not pass starts the cooldown for its player.
func forgetSubstituteVote(id uint, cool bool) (*substituteVote, bool) {
	substituteVotes.mu.Lock()
	v, ok := substituteVotes.open[id]
	delete(substituteVotes.open, id)
	if ok && cool {
		substituteVotes.cooldown[substituteKey(id, v.out)] = time.Now().Add(substituteCooldown)
	}
	substituteVotes.mu.Unlock()
	if ok && v.stop != nil {
		v.stop()
	}
	return v, ok
}

// closeSubstituteVote ends the game's open vote and rewrites its message (for closes nobody clicked: the timer, or the player joining).
func (m *GameManager) closeSubstituteVote(id uint, title, text string, cool bool) {
	v, ok := forgetSubstituteVote(id, cool)
	if !ok {
		return
	}
	if s := base.Session.Load(); s != nil {
		empty := []discordgo.MessageComponent{}
		embeds := []*discordgo.MessageEmbed{substituteClosedEmbed(title, text)}
		_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{Channel: v.channel, ID: v.message, Embeds: &embeds, Components: &empty})
	}
}

// ============================================================================
// Voting

func init() { helpers.RegisterComponentHandler(handleSubstituteClick) }

// handleSubstituteClick counts a press of Agree or Disagree: the new tally, or the result.
func handleSubstituteClick(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, substitutePrefix) {
		return false
	}
	parts := strings.Split(customID, "|")
	if len(parts) != 4 {
		return false
	}
	raw, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return false
	}
	gameID, out := uint(raw), parts[3]
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

	substituteVotes.mu.Lock()
	v, open := substituteVotes.open[gameID]
	substituteVotes.mu.Unlock()
	game, live := Games.Get(gameID)
	if !open || v.out != out || !live {
		update(substituteClosedEmbed("Substitute Vote Closed", "This vote is already over."), []discordgo.MessageComponent{})
		return true
	}
	actor := helpers.InteractionUserID(i)
	if !slices.Contains(v.voters, actor) {
		say("Only the other players of this game can vote on this.")
		return true
	}

	substituteVotes.mu.Lock()
	v.ballots[actor] = parts[1] == "yes"
	yes, no := v.counts()
	substituteVotes.mu.Unlock()
	needed := substituteVotesNeeded(len(v.voters))

	switch {
	case len(yes) >= needed:
		in, err := Games.SubstituteHosted(i.GuildID, gameID, out)
		if err != nil {
			text := "The player could not be swapped: " + err.Error()
			if errors.Is(err, ErrNoSubstitute) {
				text = "Nobody is waiting in the queue to take the place anymore, so nothing changes."
			}
			forgetSubstituteVote(gameID, true)
			update(substituteClosedEmbed("Substitute Vote Failed", text), []discordgo.MessageComponent{})
			return true
		}
		forgetSubstituteVote(gameID, false)
		update(substituteClosedEmbed("Substitute Vote Passed", fmt.Sprintf("<@%s> was swapped for <@%s> (%d of %d players agreed). <@%s> is now in the party queue for this game, and <@%s> is back in the waiting channel.",
			out, in.UserID, len(yes), len(v.voters), in.UserID, out)), []discordgo.MessageComponent{})
	case len(v.voters)-len(no) < needed: // even if everyone left agreed, it could not pass
		text := fmt.Sprintf("Too many players disagreed, so <@%s> stays on the team.", out)
		forgetSubstituteVote(gameID, true)
		update(substituteClosedEmbed("Substitute Vote Failed", text), []discordgo.MessageComponent{})
	default:
		update(substitutePanel(game, v, 0, false), substituteButtons(gameID, out))
	}
	return true
}

// ============================================================================
// Swapping

type substituteChoice struct {
	userID   string
	playerID uint
	username string
}

// pickSubstitute draws a random player from those waiting in the game's queue who can still play: not in a game, not already in this
// one, and registered.
func (m *GameManager) pickSubstitute(g Game) (substituteChoice, bool) {
	fn := m.fx().WaitingPlayers
	if fn == nil {
		return substituteChoice{}, false
	}
	waiting := fn(g)
	rand.Shuffle(len(waiting), func(i, j int) { waiting[i], waiting[j] = waiting[j], waiting[i] })
	for _, id := range waiting {
		if _, in := g.Player(id); in || m.InGame(g.GuildID, id) {
			continue
		}
		pid, err := m.api().PlayerID(g.GuildID, id)
		if err != nil {
			continue
		}
		name := id
		if view, err := database.Must().Player.Get(g.GuildID, id); err == nil && view.Config != nil && strings.TrimSpace(view.Config.Username) != "" {
			name = view.Config.Username
		}
		return substituteChoice{userID: id, playerID: pid, username: name}, true
	}
	return substituteChoice{}, false
}

// SubstituteHosted swaps a player of a game that is still being set up for a random player waiting in its queue: the newcomer takes the
// same team and place, the roster, the database rows, the channel access and the voice channels follow, and the engine is told. It
// returns the newcomer.
func (m *GameManager) SubstituteHosted(guildID string, id uint, outID string) (GamePlayer, error) {
	lg := m.liveIn(guildID, id)
	if lg == nil {
		return GamePlayer{}, ErrGameNotFound
	}
	g := m.snapshot(lg)
	if g.State != GamePreGame || g.Custom != nil {
		return GamePlayer{}, ErrWrongState
	}
	old, ok := g.Player(outID)
	if !ok {
		return GamePlayer{}, ErrNotParticipant
	}
	pick, ok := m.pickSubstitute(g)
	if !ok {
		return GamePlayer{}, ErrNoSubstitute
	}

	row := participantRow(Game{}, GamePlayer{PlayerID: pick.playerID, Team: old.Team, Captain: old.Captain})
	if err := m.api().AddParticipant(sdk.SystemActor, guildID, id, row); err != nil {
		return GamePlayer{}, fmt.Errorf("add the substitute: %w", err)
	}

	rating := m.deps.Elo.Ratings(guildID, []string{pick.userID})[pick.userID]
	lg.mu.Lock()
	idx := slices.IndexFunc(lg.g.Players, func(p GamePlayer) bool { return p.UserID == outID })
	if idx < 0 || lg.g.State != GamePreGame {
		lg.mu.Unlock()
		_ = m.api().RemoveParticipant(sdk.SystemActor, guildID, id, pick.playerID) // the game moved on: undo
		return GamePlayer{}, ErrWrongState
	}
	in := GamePlayer{UserID: pick.userID, Username: pick.username, PlayerID: pick.playerID, Team: old.Team, Captain: old.Captain, EloBefore: rating}
	lg.g.Players[idx] = in
	// A party that lost a member can't be assumed intact: drop groups touching the one who left.
	lg.g.Parties = slices.DeleteFunc(lg.g.Parties, func(pg partymgr.PartyGroup) bool { return slices.Contains(pg.Members, outID) })
	if lg.g.Host == outID {
		lg.g.Host = m.pickHost(lg.g)
	}
	m.mu.Lock()
	delete(m.byPlayer, playerKey(guildID, outID))
	m.mu.Unlock()
	m.indexLocked(lg)
	after := lg.g.clone()
	lg.mu.Unlock()

	if err := m.api().RemoveParticipant(sdk.SystemActor, guildID, id, old.PlayerID); err != nil {
		m.logf("remove %s from game %d after the swap: %v", outID, id, err)
	}
	m.sync(lg)
	if fn := m.fx().SwapMembers; fn != nil {
		if err := fn(after, old, in); err != nil {
			m.logf("move players for the swap in game %d: %v", id, err)
		}
	}
	if link, ok := HostEngine.(interface {
		Replaced(g Game, out string, in GamePlayer)
	}); ok {
		link.Replaced(after, outID, in)
	}
	return in, nil
}
