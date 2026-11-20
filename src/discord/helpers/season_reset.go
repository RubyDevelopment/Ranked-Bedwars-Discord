package helpers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers/base"
)

// ============================================================================
// Server reset (dashboard)
// ============================================================================
//
// POST /live/reset (same LIVE_CHAT_SECRET as the live console) with {"guild", "action": "start"|"status"|"cancel", "actor"}.
// "start" re-syncs every registered player's rank role and server nickname while no season is active (everyone counts as 0 ELO, the first rank),
// which comes out as "[0] username | nickname". It changes no stats and records no wipe.
// The dashboard polls "status" to draw the live progress. Jobs are kept in memory, one per server; a restart forgets them.

const (
	resetPageSize   = 200
	resetRecentKeep = 60

	resetStartLimit  = 3                // a server may start this many resets...
	resetStartWindow = 30 * time.Minute // ...in this long (cancelled and failed ones count too)
)

var (
	resetMu     sync.Mutex // guards resetStarts and makes "is one running / may another start" one decision
	resetStarts = map[string][]time.Time{}
)

// resetError carries the HTTP status ServeReset answers with: 409 when one is running or games are open, 429 over the limit.
type resetError struct {
	status int
	msg    string
}

func (e *resetError) Error() string { return e.msg }

// resetQuota prunes and returns how many starts the server has left and when the oldest one in the window frees a slot (0 = none used).
// Call with resetMu held.
func resetQuota(guildID string, now time.Time) (left int, nextAt int64) {
	kept := resetStarts[guildID][:0]
	for _, t := range resetStarts[guildID] {
		if now.Sub(t) < resetStartWindow {
			kept = append(kept, t)
		}
	}
	resetStarts[guildID] = kept
	if len(kept) > 0 {
		nextAt = kept[0].Add(resetStartWindow).UnixMilli()
	}
	return max(resetStartLimit-len(kept), 0), nextAt
}

// ResetEvent is one line of the live feed.
type ResetEvent struct {
	At      int64    `json:"at"`
	Player  string   `json:"player"`
	Outcome string   `json:"outcome"` // done | skipped | failed
	Detail  string   `json:"detail"`
	Steps   []string `json:"steps,omitempty"`
}

// ResetCurrent is the player being processed right now.
type ResetCurrent struct {
	Player  string `json:"player"`
	Discord string `json:"discord"`
	Step    string `json:"step"` // sync | ""
	Message string `json:"message"`
}

// ResetStatus is what the dashboard draws.
type ResetStatus struct {
	State      string        `json:"state"` // idle | running | done | cancelled | failed
	Stage      string        `json:"stage"` // loading | resetting | finished
	Error      string        `json:"error,omitempty"`
	StartedAt  int64         `json:"startedAt,omitempty"`
	FinishedAt int64         `json:"finishedAt,omitempty"`
	Actor      string        `json:"actor,omitempty"`
	Scope      string        `json:"scope,omitempty"`  // prior | all
	Season     string        `json:"season,omitempty"` // the prior season's name, when the scope is "prior"
	Rank       string        `json:"rank,omitempty"`
	Total      int           `json:"total"`
	Processed  int           `json:"processed"`
	Renamed    int           `json:"renamed"`
	RolesFixed int           `json:"rolesFixed"`
	Skipped    int           `json:"skipped"`
	Failed     int           `json:"failed"`
	Cancelling bool          `json:"cancelling,omitempty"` // a stop was asked for; the current player is being finished
	StartsLeft int           `json:"startsLeft"`           // resets this server may still start in the window
	Limit      int           `json:"limit"`
	WindowMin  int           `json:"windowMinutes"`
	NextSlotAt int64         `json:"nextSlotAt,omitempty"` // when the oldest start in the window expires and frees a slot
	Current   *ResetCurrent `json:"current,omitempty"`
	Recent     []ResetEvent  `json:"recent"` // newest first
}

type resetJob struct {
	mu     sync.Mutex
	st     ResetStatus
	cancel chan struct{}
}

var resetJobs sync.Map // guild id -> *resetJob

func (j *resetJob) snapshot() ResetStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.st
	if j.st.Current != nil {
		c := *j.st.Current
		out.Current = &c
	}
	out.Recent = append([]ResetEvent{}, j.st.Recent...) // never nil: it is a JSON list the page reads the length of
	return out
}

func (j *resetJob) update(f func(*ResetStatus)) {
	j.mu.Lock()
	f(&j.st)
	j.mu.Unlock()
}

func (j *resetJob) cancelled() bool {
	select {
	case <-j.cancel:
		return true
	default:
		return false
	}
}

// ServeReset is POST /live/reset.
func ServeReset(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("LIVE_CHAT_SECRET")
	if secret == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(given), []byte(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 2048))
	var req struct {
		Guild  string `json:"guild"`
		Action string `json:"action"`
		Actor  string `json:"actor"`
		Scope  string `json:"scope"` // "prior" (default): players of the previous season; "all": every registered player
	}
	if json.Unmarshal(raw, &req) != nil || !base.GuildIDPattern.MatchString(req.Guild) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !base.GuildIDPattern.MatchString(req.Actor) {
		req.Actor = "dashboard"
	}

	var status ResetStatus
	switch req.Action {
	case "start":
		var err error
		if status, err = startReset(req.Guild, req.Actor, req.Scope); err != nil {
			code := http.StatusConflict
			var re *resetError
			if errors.As(err, &re) {
				code = re.status
			}
			http.Error(w, err.Error(), code)
			return
		}
	case "cancel":
		if v, ok := resetJobs.Load(req.Guild); ok {
			j := v.(*resetJob)
			j.mu.Lock()
			if j.st.State == "running" {
				j.st.Cancelling = true
				select {
				case <-j.cancel:
				default:
					close(j.cancel)
				}
			}
			j.mu.Unlock()
		}
		fallthrough
	case "status":
		status = ResetStatus{State: "idle", Recent: []ResetEvent{}}
		if v, ok := resetJobs.Load(req.Guild); ok {
			status = v.(*resetJob).snapshot()
		}
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	resetMu.Lock()
	status.StartsLeft, status.NextSlotAt = resetQuota(req.Guild, time.Now())
	resetMu.Unlock()
	status.Limit, status.WindowMin = resetStartLimit, int(resetStartWindow/time.Minute)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// OpenGamesInGuild counts the games being set up, played or awaiting scoring; the game code sets it when it loads.
var OpenGamesInGuild = func(guildID string) int { return 0 }

func startReset(guildID, actor, scope string) (ResetStatus, error) {
	if scope != "all" {
		scope = "prior"
	}
	s := appearanceSession.Load()
	if s == nil {
		return ResetStatus{}, errors.New("the bot is not ready yet")
	}
	// One decision under one lock: a second click or a second tab cannot slip past the running check or the limit.
	resetMu.Lock()
	defer resetMu.Unlock()
	if v, ok := resetJobs.Load(guildID); ok && v.(*resetJob).snapshot().State == "running" {
		return ResetStatus{}, &resetError{http.StatusConflict, "A reset is already running for this server. Wait for it to finish or stop it first."}
	}
	now := time.Now()
	if left, nextAt := resetQuota(guildID, now); left == 0 {
		wait := time.Until(time.UnixMilli(nextAt)).Round(time.Minute)
		return ResetStatus{}, &resetError{http.StatusTooManyRequests, fmt.Sprintf("This server has already started %d resets in the last %d minutes. Try again in about %d minute(s).",
			resetStartLimit, int(resetStartWindow/time.Minute), max(int(wait/time.Minute), 1))}
	}
	if n := OpenGamesInGuild(guildID); n > 0 {
		return ResetStatus{}, &resetError{http.StatusConflict, fmt.Sprintf("%d game(s) are still open (waiting, being played or awaiting scoring): finish or void them first", n)}
	}
	resetStarts[guildID] = append(resetStarts[guildID], now)
	j := &resetJob{cancel: make(chan struct{}), st: ResetStatus{State: "running", Stage: "loading", StartedAt: now.UnixMilli(), Actor: actor, Scope: scope, Recent: []ResetEvent{}}}
	resetJobs.Store(guildID, j)
	go runReset(j, guildID, scope)
	return j.snapshot(), nil
}

func (j *resetJob) event(e ResetEvent) {
	e.At = time.Now().UnixMilli()
	j.update(func(st *ResetStatus) {
		st.Processed++
		switch e.Outcome {
		case "skipped":
			st.Skipped++
		case "failed":
			st.Failed++
		}
		st.Recent = append([]ResetEvent{e}, st.Recent...)
		if len(st.Recent) > resetRecentKeep {
			st.Recent = st.Recent[:resetRecentKeep]
		}
	})
}

func (j *resetJob) finish(state, errMsg string) {
	j.update(func(st *ResetStatus) {
		st.State, st.Stage, st.Error, st.Current = state, "finished", errMsg, nil
		st.FinishedAt = time.Now().UnixMilli()
	})
}

func runReset(j *resetJob, guildID, scope string) {
	s := appearanceSession.Load()
	if j.cancelled() {
		j.finish("cancelled", "")
		return
	}
	// This resets roles, rank and nicknames only; it never touches stats or records a wipe. With no season active every player counts
	// as 0 ELO (the first rank), which is exactly what the sync below applies. With a season active their real ELO would win, so
	// refuse instead of quietly doing something else.
	seasonKey, seasonID, err := ActiveSeasonKey(guildID)
	if err != nil {
		j.finish("failed", "Could not check the server's seasons.")
		return
	}
	if seasonID != 0 {
		j.finish("failed", fmt.Sprintf("Season %s is still active. End it (Season Periods) before resetting: this reset only clears roles and nicknames, it does not wipe stats.", seasonKey))
		return
	}
	ranks, err := GuildMetaRanks(guildID)
	if err != nil {
		j.finish("failed", "Could not load the server's ranks.")
		return
	}
	rankName, _ := LowestRankDefaults(ranks)
	if rankName == "" {
		rankName = "Unranked"
	}
	// Who is reset: everyone registered, or only the players who took part in the previous season.
	var total int
	var page func(offset int) ([]sdk.PlayerView, error)
	if scope == "all" {
		_, total, err = database.GuildPlayers(guildID, 1, 0)
		page = func(offset int) ([]sdk.PlayerView, error) {
			rows, _, err := database.GuildPlayers(guildID, resetPageSize, offset)
			return rows, err
		}
	} else {
		priorID, priorName, perr := database.PriorSeason(guildID)
		if perr != nil {
			j.finish("failed", "Could not load the server's seasons: "+perr.Error())
			return
		}
		if priorID == 0 {
			j.finish("failed", "There is no previous season to take players from. Choose \"All players\" instead.")
			return
		}
		j.update(func(st *ResetStatus) { st.Season = priorName })
		_, total, err = database.SeasonPlayers(guildID, priorID, 1, 0)
		page = func(offset int) ([]sdk.PlayerView, error) {
			ids, _, err := database.SeasonPlayers(guildID, priorID, resetPageSize, offset)
			if err != nil {
				return nil, err
			}
			views := make([]sdk.PlayerView, 0, len(ids))
			for _, id := range ids {
				if v, gerr := database.Must().Player.Get(guildID, sdk.UintToString(id)); gerr == nil && v != nil {
					views = append(views, *v)
				} else {
					views = append(views, sdk.PlayerView{Config: &models.PlayerConfigsModel{Username: fmt.Sprintf("player #%d", id)}}) // skipped below: no Discord account
				}
			}
			return views, nil
		}
	}
	if err != nil {
		j.finish("failed", "Could not load the players: "+err.Error())
		return
	}
	j.update(func(st *ResetStatus) { st.Stage, st.Rank, st.Total = "resetting", rankName, total })

	for offset := 0; offset < total; offset += resetPageSize {
		if j.cancelled() {
			j.finish("cancelled", "")
			return
		}
		rows, err := page(offset)
		if err != nil {
			j.finish("failed", "Could not load the players: "+err.Error())
			return
		}
		for _, row := range rows {
			if j.cancelled() {
				j.finish("cancelled", "")
				return
			}
			resetOnePlayer(j, s, guildID, row)
		}
	}
	j.finish("done", "")
}

// resetOnePlayer re-syncs one player's registered role, rank role and nickname. No stats are changed and nothing is recorded as a wipe.
func resetOnePlayer(j *resetJob, s *discordgo.Session, guildID string, row sdk.PlayerView) {
	name, discordID := row.Config.Username, ""
	if row.Player != nil {
		discordID = row.Player.SnowflakeID
	}
	if row.Player == nil || discordID == "" {
		j.event(ResetEvent{Player: name, Outcome: "skipped", Detail: "No Discord account linked"})
		return
	}
	step := func(k, msg string) {
		j.update(func(st *ResetStatus) { st.Current = &ResetCurrent{Player: name, Discord: discordID, Step: k, Message: msg} })
	}
	var steps []string

	// Re-sync the registered role, rank role and "[0] username | nickname".
	step("sync", "Updating rank role and nickname")
	if _, err := FetchGuildMember(s, guildID, discordID); err != nil {
		j.event(ResetEvent{Player: name, Outcome: "skipped", Detail: "Not in the server any more", Steps: steps})
		return
	}
	actions, err := SyncRegisteredPlayerProfile(s, guildID, row.Player.ID, discordID)
	if err != nil {
		j.event(ResetEvent{Player: name, Outcome: "failed", Detail: "Sync failed: " + err.Error(), Steps: steps})
		return
	}
	for _, a := range actions {
		a = strings.ReplaceAll(a, "**", "")
		switch {
		case strings.HasPrefix(a, "Updated server nickname"):
			j.update(func(st *ResetStatus) { st.Renamed++ })
		case strings.HasPrefix(a, "Synchronized rank role"), strings.HasPrefix(a, "Assigned"):
			j.update(func(st *ResetStatus) { st.RolesFixed++ })
		}
		steps = append(steps, a)
	}
	detail := "Already up to date"
	if len(actions) > 0 {
		detail = fmt.Sprintf("Reset (%d changes)", len(steps))
	}
	j.event(ResetEvent{Player: name, Outcome: "done", Detail: detail, Steps: steps})
}
