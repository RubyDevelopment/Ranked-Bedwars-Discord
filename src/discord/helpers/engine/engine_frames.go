package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/blob"
	"ranked-bedwars/src/discord/helpers/match"
	"strconv"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

func (b *EngineBridge) handle(msg engineInbound) {
	switch msg.Type {
	case "hello":
		b.logf("engine ready")
		if !blob.Blobs.Enabled() {
			b.logf("WARNING: BLOB_READ_WRITE_TOKEN is not set, so the chat logs of hosted games cannot be saved and will not show on the dashboard")
		}
		b.sendResumes()

	case "host.resumed":
		b.resumed(msg.GameID, msg.Bot)

	case "host.gone":
		b.gone(msg.GameID)

	case "host.accepted":
		b.reply(msg.GameID, hostReply{handled: true, bot: msg.Bot})
		if g, ok := match.Games.Get(msg.GameID); ok {
			_ = match.Games.SetBot(g.ID, msg.Bot)
		}

	case "host.maps":
		b.mu.Lock()
		selectMap := b.mapSelectors[msg.GameID]
		delete(b.mapSelectors, msg.GameID)
		b.mu.Unlock()
		go func() {
			var selection *match.MapSelection
			var err error
			if selectMap == nil {
				err = errors.New("the game has no map-voting handler")
			} else {
				selection, err = selectMap(msg.Maps, msg.MapMenuRead)
				if err == nil && selection == nil {
					err = errors.New("no configured maps are available in the selected mode")
				}
			}
			frame := map[string]any{"type": "host.map.selected", "game_id": msg.GameID}
			if err != nil {
				frame["error"] = err.Error()
			} else {
				frame["map"] = selection
			}
			if writeErr := b.Write(frame); writeErr != nil {
				b.logf("send map selection for game %d: %v", msg.GameID, writeErr)
			}
		}()

	case "host.map.vote": // a player voted for a map in the Minecraft party chat
		match.Games.Notify(msg.GameID, match.GameEvent{Kind: match.EventChatVote, Key: msg.Voter, Vars: map[string]string{"number": strconv.Itoa(msg.Number)}})

	case "host.map.hosted":
		if msg.Map != nil {
			if err := match.Games.SetMap(msg.GameID, msg.Map.Name); err != nil && !errors.Is(err, match.ErrGameNotFound) {
				b.logf("record hosted map for game %d: %v", msg.GameID, err)
			}
		}

	case "host.failed":
		b.hostFailed(msg)

	case "verify.result":
		b.mu.Lock()
		ch := b.verifies[msg.RequestID]
		b.mu.Unlock()
		if ch != nil {
			select {
			case ch <- verifyReply{result: VerifyResult{Status: msg.Status, Reason: msg.Reason}}:
			default:
			}
		}

	case "host.regroup":
		go func() {
			teams := map[string][]string{}
			for _, t := range msg.Teams {
				teams[t.Name] = t.IDs
			}
			if g, ok := match.Games.Get(msg.GameID); ok {
				if err := match.Games.Regroup(g.GuildID, g.ID, teams); err != nil {
					b.logf("regroup game %d: %v", msg.GameID, err)
					return
				}
			}
		}()

	case "players.sync":
		go b.syncPlayers(msg.GameID)

	case "host.party.stalled":
		for _, p := range msg.Players { // who is still not in the party after several rounds of invites
			go match.Games.OfferSubstitute(msg.GameID, p.ID, p.Rounds, p.Offline)
		}

	case "host.party.recovered":
		go match.Games.CancelSubstituteVote(msg.GameID, msg.PlayerID)

	case "host.action.result":
		b.mu.Lock()
		ch := b.actions[msg.RequestID]
		b.mu.Unlock()
		if ch != nil {
			select {
			case ch <- match.HostActionResult{OK: msg.OK, Command: msg.Command, Error: msg.Error, Cooldown: msg.Cooldown}:
			default:
			}
		}

	case "host.status":
		match.Games.Notify(msg.GameID, match.GameEvent{Kind: match.EventHostStatus, Stage: msg.Stage, Reason: msg.Detail, Waiting: msg.Waiting})

	case "host.embed":
		if msg.Key == "mapSelected" {
			if name := strings.TrimSpace(msg.Vars["map"]); name != "" {
				if err := match.Games.SetMap(msg.GameID, name); err != nil {
					b.logf("record selected map for game %d: %v", msg.GameID, err)
				}
			}
		}
		match.Games.Notify(msg.GameID, match.GameEvent{Kind: match.EventEmbed, Key: msg.Key, Vars: msg.Vars, JoinedIDs: msg.JoinedIDs, WaitingIDs: msg.WaitingIDs, InvitedIDs: msg.InvitedIDs})

	case "game.event":
		match.Games.Notify(msg.GameID, match.GameEvent{Kind: match.EventMatch, Match: &match.MatchEvent{
			Kind: msg.Kind, OccurredAt: timelineTime(msg.At), Killer: msg.Killer, Victim: msg.Victim, Final: msg.Final, FirstBlood: msg.FirstBlood, Team: msg.Team, Destroyer: msg.Destroyer,
			Phase: msg.Phase, PhaseName: msg.PhaseName, Stat: msg.Stat, Player: msg.StatPlayer, Amount: msg.StatAmount, Total: msg.StatTotal,
		}})
		b.persistTimelineEvent(msg.GameID, b.timelineInput(msg))
		// Out of the match for good: free to queue again right away.
		switch {
		case msg.Kind == timelineKill && msg.Final && msg.Victim != "":
			match.Games.ReleasePlayers(msg.GameID, msg.Victim)
		case msg.Kind == timelineEliminated && msg.Team != "":
			if g, ok := match.Games.Get(msg.GameID); ok {
				var ids []string
				for _, p := range g.TeamPlayers(msg.Team) {
					ids = append(ids, p.UserID)
				}
				match.Games.ReleasePlayers(msg.GameID, ids...)
			}
		}

	case "game.started":
		if err := match.Games.BeginMatch(msg.GameID); err != nil && !errors.Is(err, match.ErrWrongState) {
			b.logf("begin match %d: %v", msg.GameID, err)
		}
		b.persistTimelineEvent(msg.GameID, timelineInput(timelineGameStarted, msg.At))

	case "chat.line":
		LiveChat.Line(msg.GuildID, msg.Bot, msg.Text, msg.Motd, msg.At)

	case "chat.bots":
		LiveChat.Bots(msg.GuildID, msg.Bots)

	case "bot.event":
		LiveChat.Event(msg.GuildID, msg.Bot, msg.Kind, msg.At, msg.Data)

	case "bot.result":
		LiveChat.Result(msg.RequestID, match.ControlResult{OK: msg.OK, Bot: msg.Bot, Data: msg.Data, Error: msg.Error})

	case "game.chat":
		match.Games.StashChat(msg.GameID, msg.Lines)

	case "game.ended":
		b.gameEnded(msg)

	case "game.tracking_lost":
		b.mu.Lock()
		delete(b.hosted, msg.GameID)
		delete(b.mapSelectors, msg.GameID)
		b.mu.Unlock()
		match.Games.Notify(msg.GameID, match.GameEvent{Kind: match.EventTrackingLost, Reason: msg.Reason})
	}
}

func (b *EngineBridge) timelineInput(msg engineInbound) timelineEventRequest {
	input := timelineEventRequest{Kind: msg.Kind, OccurredAt: timelineTime(msg.At)}
	switch msg.Kind {
	case timelineKill:
		input.Actor, input.Target = optionalText(msg.Killer), optionalText(msg.Victim)
		input.Final, input.FirstBlood = optionalBool(msg.Final), optionalBool(msg.FirstBlood)
	case timelineBed:
		input.Team, input.Actor = optionalText(msg.Team), optionalText(msg.Destroyer)
	case timelineEliminated:
		input.Team = optionalText(msg.Team)
	case timelineStat:
		input.Stat, input.Actor = optionalText(msg.Stat), optionalText(msg.StatPlayer)
		input.Amount, input.Total = optionalNumber(msg.StatAmount), optionalNumber(msg.StatTotal)
	case timelinePhase:
		input.Phase, input.PhaseName = optionalInt(msg.Phase), optionalText(msg.PhaseName)
	}
	return input
}

func (b *EngineBridge) persistTimelineEvent(gameID uint, input timelineEventRequest) {
	guildID := ""
	if g, ok := match.Games.Get(gameID); ok {
		guildID = g.GuildID
	} else {
		b.mu.Lock()
		guildID = b.hosted[gameID]
		b.mu.Unlock()
	}
	if guildID == "" {
		b.logf("cannot persist timeline event for game %d: guild is unavailable", gameID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := fmt.Sprintf("/v1/guilds/%s/games/%d/events", url.PathEscape(guildID), gameID)
	var persisted json.RawMessage
	if err := database.Must().Client().API.Do(ctx, http.MethodPost, path, input, &persisted); err != nil {
		b.logf("persist timeline event for game %d: %v", gameID, err)
	}
}

func timelineInput(kind string, at int64) timelineEventRequest {
	return timelineEventRequest{Kind: kind, OccurredAt: timelineTime(at)}
}

func timelineTime(at int64) time.Time {
	if at <= 0 {
		return time.Now().UTC()
	}
	return time.UnixMilli(at).UTC()
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalBool(value bool) *bool         { return &value }
func optionalInt(value int) *int            { return &value }
func optionalNumber(value float64) *float64 { return &value }

func (b *EngineBridge) reply(gameID uint, r hostReply) {
	b.mu.Lock()
	ch := b.replies[gameID]
	b.mu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}

// hostFailed: before the engine accepted the game, "no bot / no server" means host by hand;
// anything else (or any failure after accepting) voids the game with the engine's reason.
func (b *EngineBridge) hostFailed(msg engineInbound) {
	b.mu.Lock()
	_, waiting := b.replies[msg.GameID]
	delete(b.hosted, msg.GameID)
	delete(b.mapSelectors, msg.GameID)
	b.mu.Unlock()

	if waiting {
		if msg.Code == "no_bot" || msg.Code == "no_server" {
			b.failureNotice(msg, "noBot")
		} else {
			b.failureNotice(msg, "")
		}
		switch {
		case msg.Then == "void": // the profile says to void a game nobody can host
			b.reply(msg.GameID, hostReply{err: errors.New(msg.Reason), void: true})
		case msg.Then == "manual" || msg.Code == "no_bot" || msg.Code == "no_server":
			b.reply(msg.GameID, hostReply{handled: false})
		default:
			// Not accepted yet: Host falls back to manual hosting. Voiding too would race with staff hosting it.
			b.reply(msg.GameID, hostReply{err: errors.New(msg.Reason)})
		}
		return
	}
	if msg.Code == "cancelled" {
		return // we asked for it
	}
	reason := strings.TrimSpace(msg.Reason)
	if reason == "" {
		reason = "The hosting bot failed."
	}
	if g, ok := match.Games.Get(msg.GameID); ok {
		manual := msg.Then == "manual"
		if msg.Embed != "" {
			b.failureNotice(msg, "") // the step's own failure embed
		}
		if msg.Diagnosis != "" || msg.Hint != "" || msg.Step != "" {
			match.Games.Notify(g.ID, match.GameEvent{Kind: match.EventEmbed, Key: "hostProblem", Vars: map[string]string{
				"reason": reason, "step": msg.Step, "diagnosis": base.Clip(msg.Diagnosis, 900), "hint": msg.Hint,
			}})
		}
		switch {
		case msg.Code == "party":
			if msg.Embed == "" {
				match.Games.Notify(g.ID, match.GameEvent{Kind: match.EventEmbed, Key: "partyTimeout", JoinedIDs: match.PlayersExcept(g, msg.Missing), WaitingIDs: msg.Missing})
			}
			if !manual {
				b.strikeNoShows(g, msg.Missing)
			}
		case msg.Code == "teams":
			if msg.Embed == "" {
				match.Games.Notify(g.ID, match.GameEvent{Kind: match.EventEmbed, Key: "teamTimeout", WaitingIDs: msg.Missing})
			}
		case msg.Embed == "":
			if key := stepFailEmbeds[msg.StepType]; key != "" {
				b.failureNotice(msg, key)
			}
		}
		if manual {
			// The profile says staff finish this game by hand: before the match, start it without the bot; during it, ask for the win.
			switch g.State {
			case match.GamePreGame:
				_ = match.Games.SetBot(g.ID, "")
				_ = match.Games.BeginMatch(g.ID)
			case match.GameInGame:
				match.Games.Notify(g.ID, match.GameEvent{Kind: match.EventTrackingLost, Reason: reason})
			}
			return
		}
		_ = match.Games.Void(sdk.SystemActor, g.GuildID, g.ID, reason, match.VoidOptions{})
	}
}

// stepFailEmbeds is the built-in embed a failing pipeline step posts when the profile did not pick one (party and teams have their own, with player lists).
var stepFailEmbeds = map[string]string{"connect": "botConnectFailed", "privateGame": "privateGameFailed", "map": "mapFailed", "settings": "settingsFailed", "start": "startFailed"}

// failureNotice posts the embed the profile chose for a failure (a built-in key, or "custom:<id>" for a custom message),
// or `fallback` when none was chosen and the failure has a default embed. An empty `fallback` posts nothing by default.
func (b *EngineBridge) failureNotice(msg engineInbound, fallback string) {
	key := base.FirstNonEmpty(msg.Embed, fallback)
	if key == "" {
		return
	}
	if _, ok := strings.CutPrefix(key, "custom:"); ok {
		key = "step:" + key // postStepReply finds custom messages under "custom:<id>"
	}
	match.Games.Notify(msg.GameID, match.GameEvent{Kind: match.EventEmbed, Key: key, Vars: map[string]string{"reason": strings.TrimSpace(msg.Reason), "step": msg.Step, "bot": msg.Bot}})
}

// strikeNoShows gives a strike to every player who never joined the party, when the mode auto-strikes.
func (b *EngineBridge) strikeNoShows(g match.Game, ids []string) {
	if !g.Mode.IsAutoStrikingEnabled || g.Custom != nil {
		return
	}
	for _, id := range ids {
		p, ok := g.Player(id)
		if !ok || p.PlayerID == 0 {
			continue
		}
		_, err := database.Must().Strikes.Create(sdk.SystemActor, g.GuildID, sdk.UintToString(p.PlayerID), sdk.NewStrike{
			StaffID: "000000000000000000",
			Reason:  fmt.Sprintf("Failed to join the party in time (automatic) - game #%d", g.Number),
		})
		if err != nil {
			b.logf("no-show strike for %s in game %d: %v", id, g.ID, err)
		}
	}
}

// gameEnded records what the engine tracked and scores the game for the team that won.
func (b *EngineBridge) gameEnded(msg engineInbound) {
	g, ok := match.Games.Get(msg.GameID)
	endEvent := timelineInput(timelineGameEnded, msg.At)
	endEvent.Winner, endEvent.Inferred = optionalText(msg.Winner), optionalBool(msg.Inferred)
	b.persistTimelineEvent(msg.GameID, endEvent)

	b.mu.Lock()
	delete(b.hosted, msg.GameID)
	delete(b.mapSelectors, msg.GameID)
	b.mu.Unlock()
	if !ok {
		b.logf("game-ended frame for game %d: runtime game is unavailable", msg.GameID)
		return
	}
	// The match is over: its players are free to queue again right away, not once the stats are stored and the result is scored.
	ids := make([]string, len(g.Players))
	for n, p := range g.Players {
		ids[n] = p.UserID
	}
	match.Games.ReleasePlayers(g.ID, ids...)
	statsFailed := false
	for _, p := range msg.Players {
		if err := match.Games.AddStats(g.ID, p.ID, match.GamePlayer{
			Kills: p.Kills, Deaths: p.Deaths, Assists: p.Assists, FinalKills: p.FinalKills,
			BedsDestroyed: p.BedsDestroyed, BedsLost: p.BedsLost, FirstBloods: p.FirstBloods, Custom: p.Custom,
		}); err != nil {
			statsFailed = true
			b.logf("store tracked stats for player %s in game %d: %v", p.ID, g.ID, err)
		}
	}
	if statsFailed {
		match.Games.Notify(g.ID, match.GameEvent{Kind: match.EventTrackingLost, Reason: "Some tracked participant statistics could not be recorded."})
	}
	if strings.TrimSpace(msg.Winner) == "" {
		match.Games.Notify(g.ID, match.GameEvent{Kind: match.EventTrackingLost, Reason: "The match ended but the bot couldn't tell who won."})
		return
	}
	match.Games.Notify(g.ID, match.GameEvent{Kind: match.EventMatchEnded, Vars: map[string]string{"winner": msg.Winner}})
	if _, err := match.Games.Score(sdk.SystemActor, g.GuildID, g.ID, msg.Winner, match.ScoreOptions{}); err != nil {
		b.logf("score game %d for %q: %v", g.ID, msg.Winner, err)
		match.Games.Notify(g.ID, match.GameEvent{Kind: match.EventTrackingLost, Reason: "The result couldn't be scored automatically: " + err.Error()})
	}
}

// ============================================================================
// Live Minecraft chat for the dashboard
