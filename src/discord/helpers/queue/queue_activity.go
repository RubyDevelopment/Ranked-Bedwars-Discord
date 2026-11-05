package queue

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"

	"ranked-bedwars/src/database"
)

// ============================================================================
// Estimated activity for an empty queue
// ============================================================================

// When nobody is in a queue channel its status used to be empty. Instead it now tells when games usually happen, worked out from the
// server's own recent games: how many games were made in each hour of each weekday over the last four weeks.
//
//	📈 Usually busy now · ~4 games/hr            the current hour is one of the server's active ones
//	💤 Quiet right now · usually picks up around 8 PM UTC · ~5 games/hr
//	(nothing)                                    too few recent games to say anything
//
// The history is read once every half hour per server (it changes slowly), not for every status.

const (
	activityWindow  = 28 * 24 * time.Hour // how far back the games are counted
	activityRefresh = 30 * time.Minute    // how often the history is read again
	activityMinimum = 12                  // fewer games than this in the window say nothing reliable
	activityPages   = 10                  // pages of 200 games read at most (2000 games)
	busyGamesPerHr  = 1.0                 // an hour with at least this many games on average counts as busy
)

// activityProfile is the average number of games per hour for every hour of the week (UTC), and how many games it was built from.
type activityProfile struct {
	perHour [7][24]float64
	games   int
}

// buildActivityProfile counts games by weekday and hour. Only whole days count (the partly passed first and last day would skew it), and
// the counts are divided by how many times each weekday occurred among them.
func buildActivityProfile(times []time.Time, now time.Time) activityProfile {
	var p activityProfile
	const day = 24 * time.Hour
	from := now.Add(-activityWindow).UTC()
	if t := from.Truncate(day); t.Before(from) {
		from = t.Add(day) // the first whole day
	}
	to := now.UTC().Truncate(day) // the end of the last whole day
	var counts [7][24]int
	for _, t := range times {
		t = t.UTC()
		if t.Before(from) || !t.Before(to) {
			continue
		}
		counts[t.Weekday()][t.Hour()]++
		p.games++
	}
	var occurrences [7]int
	for d := from; d.Before(to); d = d.Add(day) {
		occurrences[d.Weekday()]++
	}
	for wd := range counts {
		for hour := range counts[wd] {
			if occurrences[wd] > 0 {
				p.perHour[wd][hour] = float64(counts[wd][hour]) / float64(occurrences[wd])
			}
		}
	}
	return p
}

// status is what an empty queue reads at this moment: "" when there is too little history to estimate anything.
func (p activityProfile) status(now time.Time) string {
	if p.games < activityMinimum {
		return ""
	}
	now = now.UTC()
	here := p.perHour[now.Weekday()][now.Hour()]
	if here >= busyGamesPerHr {
		return fmt.Sprintf("📈 Usually busy now · ~%s games/hr", rate(here))
	}
	// The next busy hour within a day, else the busiest hour of the next day.
	best, bestAt := 0.0, -1
	for ahead := 1; ahead <= 24; ahead++ {
		t := now.Add(time.Duration(ahead) * time.Hour)
		v := p.perHour[t.Weekday()][t.Hour()]
		if v >= busyGamesPerHr {
			return fmt.Sprintf("💤 Quiet right now · usually picks up around %s · ~%s games/hr", clock(t), rate(v))
		}
		if v > best {
			best, bestAt = v, ahead
		}
	}
	if bestAt > 0 && best > 0 {
		t := now.Add(time.Duration(bestAt) * time.Hour)
		return fmt.Sprintf("💤 Quiet right now · busiest around %s", clock(t))
	}
	return "💤 Quiet right now"
}

// rate writes a games-per-hour figure without a needless decimal.
func rate(v float64) string {
	if v >= 10 || v == float64(int(v)) {
		return strconv.Itoa(int(v + 0.5))
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// clock is an hour as "8 PM UTC": a voice channel status is plain text, so it cannot show each reader's own time zone.
func clock(t time.Time) string {
	h := t.UTC().Hour()
	suffix := "AM"
	if h >= 12 {
		suffix = "PM"
	}
	if h%12 == 0 {
		return "12 " + suffix + " UTC"
	}
	return fmt.Sprintf("%d %s UTC", h%12, suffix)
}

// ============================================================================
// History
// ============================================================================

type cachedProfile struct {
	profile activityProfile
	at      time.Time
	loading bool
}

var (
	activityMu    sync.Mutex
	activityCache = map[string]*cachedProfile{}
)

// gameTimesSince lists when the guild's games were made since the given time; a variable so tests can supply their own.
var gameTimesSince = func(guildID string, since time.Time) ([]time.Time, error) {
	svc := database.Get()
	if svc == nil {
		return nil, fmt.Errorf("database not ready")
	}
	var out []time.Time
	for page := 0; page < activityPages; page++ {
		q := url.Values{"since": {since.UTC().Format(time.RFC3339)}, "limit": {"200"}, "offset": {strconv.Itoa(page * 200)}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		list, err := svc.Client().ListGames(ctx, guildID, q)
		cancel()
		if err != nil {
			return out, err
		}
		for _, g := range list.Data {
			if g.StartedAt != nil {
				out = append(out, *g.StartedAt)
			} else {
				out = append(out, g.CreatedAt)
			}
		}
		if len(list.Data) < 200 {
			break
		}
	}
	return out, nil
}

// estimatedActivity is the status line for an empty queue in this server. The history is read in the background once it is older than
// half an hour; until the first read finishes (and if reading fails) the answer is "" and the channel shows no status, as before.
func estimatedActivity(guildID string) string {
	now := time.Now()
	activityMu.Lock()
	c := activityCache[guildID]
	if c == nil {
		c = &cachedProfile{}
		activityCache[guildID] = c
	}
	stale := c.at.IsZero() || now.Sub(c.at) > activityRefresh
	if stale && !c.loading {
		c.loading = true
		go refreshActivity(guildID, c)
	}
	profile, known := c.profile, !c.at.IsZero()
	activityMu.Unlock()
	if !known {
		return ""
	}
	return profile.status(now)
}

func refreshActivity(guildID string, c *cachedProfile) {
	defer func() {
		if r := recover(); r != nil {
			queueLogger.Error(fmt.Sprintf("reading the activity history of %s failed: %v", guildID, r))
		}
		activityMu.Lock()
		c.loading = false
		activityMu.Unlock()
	}()
	now := time.Now()
	times, err := gameTimesSince(guildID, now.Add(-activityWindow))
	if err != nil && len(times) == 0 {
		queueLogger.Warn(fmt.Sprintf("could not read the recent games of %s for the queue activity estimate: %v", guildID, err))
		activityMu.Lock()
		if !c.at.IsZero() { // keep the old numbers, try again in a while
			c.at = now.Add(-activityRefresh + 2*time.Minute)
		}
		activityMu.Unlock()
		return
	}
	p := buildActivityProfile(times, now)
	activityMu.Lock()
	c.profile, c.at = p, now
	activityMu.Unlock()
}
