package match

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/ratings"
	"ranked-bedwars/src/types"
	"slices"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

type mapVote struct {
	maps      []types.Map
	votes     map[string]int // user -> map index
	weights   map[string]float64
	message   *discordgo.Message
	done      chan struct{}
	closeOnce sync.Once
	game      Game
	weighted  bool  // the winner is drawn by vote share instead of the most votes
	endsAt    int64 // when the vote closes (Unix seconds)
}

// selectMap votes only for configured maps that are present in the selected mode's live menu.
// Each vote is worth the voter's best perk map-vote multiplier.
func (d *gameDiscord) selectMap(g Game, available []MapOption, mapMenuRead bool) (*MapSelection, error) {
	s := d.session()
	if s == nil || g.Channels.Text == "" {
		return nil, nil
	}
	all, err := helpers.GuildMetaMaps(g.GuildID)
	if err != nil {
		return nil, err
	}
	availableSlots := map[string]int{}
	for _, option := range available {
		if name := strings.ToLower(strings.TrimSpace(option.Name)); name != "" {
			availableSlots[name] = option.GUISlot
		}
	}
	var maps []types.Map
	for _, m := range all {
		if m.IsEnabled && m.IsMapAvailable && strings.TrimSpace(m.Name) != "" {
			if mapMenuRead {
				slot, found := availableSlots[strings.ToLower(strings.TrimSpace(m.Name))]
				if !found {
					continue
				}
				m.GUISlot = slot
			}
			maps = append(maps, m)
		}
	}
	// Prefer maps configured with enough team colours, but retain the legacy list if none meet that threshold.
	teams, _ := TeamLayout(g.Mode)
	maps = mapsWithEnoughTeams(maps, g.Mode.Name, teams)
	if len(maps) == 0 {
		d.post(s, g, "noMaps", embedData{})
		return nil, nil
	}
	if len(maps) > maxMapVoteOptions {
		rand.Shuffle(len(maps), func(i, j int) { maps[i], maps[j] = maps[j], maps[i] })
		maps = maps[:maxMapVoteOptions]
	}
	if len(maps) == 1 {
		return mapSelectionWithFallbacks(maps, maps[0], g.Mode.Name), nil
	}

	lasts, perks, weighted, voting := flowOf(g.GuildID).mapVoteRules()
	if !voting {
		return mapSelectionWithFallbacks(maps, maps[rand.IntN(len(maps))], g.Mode.Name), nil
	}
	// The perks are looked up first (they are database reads that can take a while), and only then is the end of the vote fixed: the
	// countdown on the message and the moment the vote really closes are the same one, so the embed never runs out before the vote does.
	weights := map[string]float64{}
	for _, p := range g.Players {
		weights[p.UserID] = 1
		if perks {
			weights[p.UserID] = ratings.Elo.PerksOf(g.GuildID, p.UserID).MapVoteWeight()
		}
	}
	deadline := time.Now().Add(lasts)
	v := &mapVote{weighted: weighted, maps: maps, votes: map[string]int{}, weights: weights, done: make(chan struct{}), game: g, endsAt: deadline.Unix()}
	embed, _ := d.voteEmbed(v, "")
	helpers.WithGuildFooterIconForGuild(s, g.GuildID, embed)
	msg, err := s.ChannelMessageSendComplex(g.Channels.Text, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}, Components: v.components(g.ID, false, -1)})
	if err != nil {
		return nil, err
	}
	v.message = msg

	d.mu.Lock()
	d.votes[g.ID] = v
	d.mu.Unlock()
	names := make([]string, len(maps))
	for i, m := range maps {
		names[i] = m.Name
	}
	_ = HostEngine.Write(map[string]any{"type": "host.map.vote.options", "game_id": g.ID, "maps": names}) // so the bot can say them in the party chat
	defer func() {
		d.mu.Lock()
		delete(d.votes, g.ID)
		d.mu.Unlock()
	}()

	select {
	case <-v.done:
	case <-time.After(time.Until(deadline)):
	}

	d.mu.Lock()
	winner := v.winner()
	d.mu.Unlock()
	embed, rows := d.voteEmbed(v, winner.Name)
	helpers.WithGuildFooterIconForGuild(s, g.GuildID, embed)
	winnerIdx := slices.IndexFunc(v.maps, func(m types.Map) bool { return m.Name == winner.Name })
	comps := v.components(g.ID, true, winnerIdx)
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel: msg.ChannelID, ID: msg.ID, Embeds: &[]*discordgo.MessageEmbed{embed}, Components: &comps,
	})
	// The bot's click on the map edits this same message ("The map selection has concluded!"), as the old bot did.
	d.mu.Lock()
	d.closedVotes[g.ID] = &closedVote{msg: msg, rows: rows}
	d.mu.Unlock()
	selected := slices.IndexFunc(maps, func(m types.Map) bool { return m.Name == winner.Name })
	if selected < 0 {
		return nil, errors.New("the winning map is no longer in the available map list")
	}
	return mapSelectionWithFallbacks(maps, maps[selected], g.Mode.Name), nil
}

func mapsWithEnoughTeams(maps []types.Map, mode string, teamCount int) []types.Map {
	playable := make([]types.Map, 0, len(maps))
	for _, m := range maps {
		if len(m.TeamsFor(mode)) >= teamCount {
			playable = append(playable, m)
		}
	}
	if len(playable) > 0 {
		return playable
	}
	return maps
}

func mapSelectionWithFallbacks(maps []types.Map, selected types.Map, mode string) *MapSelection {
	selection := mapSelection(selected, mode)
	seenNames := map[string]struct{}{strings.ToLower(strings.TrimSpace(selected.Name)): {}}
	seenSlots := map[int]struct{}{selected.GUISlot: {}}
	for _, candidate := range maps {
		name := strings.ToLower(strings.TrimSpace(candidate.Name))
		if name == "" {
			continue
		}
		if _, seen := seenNames[name]; seen {
			continue
		}
		if _, seen := seenSlots[candidate.GUISlot]; seen {
			continue
		}
		seenNames[name] = struct{}{}
		seenSlots[candidate.GUISlot] = struct{}{}
		selection.Fallbacks = append(selection.Fallbacks, *mapSelection(candidate, mode))
	}
	return selection
}

func mapSelection(m types.Map, mode string) *MapSelection {
	return &MapSelection{
		Name:      m.Name,
		GUISlot:   m.GUISlot,
		MapHeight: m.MapHeight,
		Teams:     m.TeamsFor(mode),
	}
}

// settled reports whether the vote can end now: everybody voted, or (when the most votes win) the leading map cannot be caught
// by the votes still to come, as the old bot ended it. Callers hold d.mu.
func (v *mapVote) settled() bool {
	if len(v.votes) >= len(v.game.Players) {
		return true
	}
	if v.weighted {
		return false
	}
	remaining := 0.0
	for _, p := range v.game.Players {
		if _, voted := v.votes[p.UserID]; !voted {
			remaining += v.weights[p.UserID]
		}
	}
	first, second := 0.0, 0.0
	for _, t := range v.totals() {
		if t > first {
			first, second = t, first
		} else if t > second {
			second = t
		}
	}
	return first > second+remaining
}

// chatVote counts a vote a player made in the Minecraft party chat (the engine passes it on) and tells the engine what to say back.
func (d *gameDiscord) chatVote(s *discordgo.Session, g Game, user string, number int) {
	d.mu.Lock()
	v := d.votes[g.ID]
	if v == nil || number < 1 || number > len(v.maps) {
		d.mu.Unlock()
		return
	}
	if _, in := v.game.Player(user); !in {
		d.mu.Unlock()
		return
	}
	idx := number - 1
	previous, had := v.votes[user]
	outcome := "voted"
	switch {
	case had && previous == idx:
		outcome = "duplicate"
	case had:
		outcome = "changed"
	}
	if outcome != "duplicate" {
		v.votes[user] = idx
	}
	done := outcome != "duplicate" && v.settled()
	name, weight, msg := v.maps[idx].Name, v.weights[user], v.message
	d.mu.Unlock()

	if outcome != "duplicate" {
		embed, _ := d.voteEmbed(v, "")
		helpers.WithGuildFooterIconForGuild(s, g.GuildID, embed)
		comps := v.components(g.ID, false, -1)
		_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{Channel: msg.ChannelID, ID: msg.ID, Embeds: &[]*discordgo.MessageEmbed{embed}, Components: &comps})
	}
	_ = HostEngine.Write(map[string]any{"type": "host.map.vote.result", "game_id": g.ID, "player": user, "outcome": outcome, "map": name, "weight": weight})
	if done {
		v.closeOnce.Do(func() { close(v.done) })
	}
}

// totals is the vote weight each map has.
func (v *mapVote) totals() []float64 {
	out := make([]float64, len(v.maps))
	for user, idx := range v.votes {
		out[idx] += v.weights[user]
	}
	return out
}

// winner is the heaviest map; ties and an empty ballot are settled by chance. In weighted mode each map's chance is its share of the votes.
func (v *mapVote) winner() types.Map {
	totals := v.totals()
	if v.weighted {
		sum := 0.0
		for _, t := range totals {
			sum += t
		}
		if sum > 0 {
			pick := rand.Float64() * sum
			for i, t := range totals {
				if pick < t {
					return v.maps[i]
				}
				pick -= t
			}
		}
	}
	best := slices.Max(totals)
	var leaders []int
	for i, t := range totals {
		if t == best {
			leaders = append(leaders, i)
		}
	}
	return v.maps[leaders[rand.IntN(len(leaders))]]
}

// voteEmbed is the map vote as the guild's "mapVoting" template draws it, or, once winner is set, the "mapVoteEnded" one.
// The vote cannot be skipped, so a template the guild switched off is drawn anyway.
func (d *gameDiscord) voteEmbed(v *mapVote, winner string) (*discordgo.MessageEmbed, []map[string]string) {
	d.mu.Lock()
	totals := v.totals()
	voted := len(v.votes)
	d.mu.Unlock()
	lines := make([]string, len(v.maps))
	order := make([]int, len(v.maps))
	for i, m := range v.maps {
		lines[i] = fmt.Sprintf("**%s** · `%s`", m.Name, formatWeight(totals[i]))
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return totals[order[a]] > totals[order[b]] })
	var mapRows []map[string]string
	for _, i := range order {
		icon, tag := "🗺️", ""
		if winner != "" && v.maps[i].Name == winner {
			icon, tag = "🏆", " [WINNER]"
		}
		mapRows = append(mapRows, map[string]string{"mapName": v.maps[i].Name, "mapIcon": icon, "mapScore": formatWeight(totals[i]), "winnerTag": tag})
	}
	vars := map[string]string{
		"endsAt": strconv.FormatInt(v.endsAt, 10), "mapVotes": strings.Join(lines, "\n"),
		"voted": strconv.Itoa(voted), "mapCount": strconv.Itoa(len(v.maps)),
	}
	key := "mapVoting"
	if winner != "" {
		key, vars["map"] = "mapVoteEnded", winner
	}
	emb, _, _ := d.build(v.game, key, embedData{Vars: vars, MapRows: mapRows, Force: true})
	if emb == nil { // no default for the key: cannot happen, but a vote must never be left without a message
		emb = &discordgo.MessageEmbed{Title: "Map Voting", Description: strings.Join(lines, "\n")}
	}
	return emb, mapRows
}

// components are the vote's buttons, each showing its map's score. Once the vote is over (winner is its map's index, else -1)
// only the maps that got votes stay, disabled, and the winner's is green.
func (v *mapVote) components(gameID uint, disabled bool, winner int) []discordgo.MessageComponent {
	var rows []discordgo.MessageComponent
	var row []discordgo.MessageComponent
	totals := v.totals()
	for i, m := range v.maps {
		if disabled && totals[i] <= 0 && i != winner {
			continue
		}
		style := discordgo.SecondaryButton
		if disabled && i == winner {
			style = discordgo.SuccessButton
		}
		row = append(row, &discordgo.Button{Label: truncateName(fmt.Sprintf("%s (%s)", m.Name, formatWeight(totals[i])), 80), Style: style, Disabled: disabled, CustomID: mapVoteIDPrefix + strconv.FormatUint(uint64(gameID), 10) + "|" + strconv.Itoa(i)})
		if len(row) == 5 {
			rows = append(rows, &discordgo.ActionsRow{Components: row})
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, &discordgo.ActionsRow{Components: row})
	}
	return rows
}

func formatWeight(w float64) string {
	w = math.Round(w*100) / 100 // 0.1+0.2 style float sums must not print as 3.5999999999999996
	return strconv.FormatFloat(w, 'f', -1, 64)
}

// ============================================================================
// Captain draft
