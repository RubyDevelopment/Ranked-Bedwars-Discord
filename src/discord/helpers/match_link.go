package helpers

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"ranked-bedwars/src/database"
)

// matchLinkTTL is how long a guild's season name is remembered. The guild's own domain is cached by the SDK's config cache.
const matchLinkTTL = 5 * time.Minute

type seasonSlugEntry struct {
	id      uint
	slug    string
	expires time.Time
}

var (
	seasonSlugMu    sync.Mutex
	seasonSlugCache = map[string]seasonSlugEntry{}
	nonSlugChars    = regexp.MustCompile(`[^a-z0-9]+`)
)

// portalRootDomain is the domain the guilds' public sites are subdomains of (PUBLIC_PORTAL_ROOT_DOMAIN, as on the website).
func portalRootDomain() string {
	if root := strings.Trim(strings.TrimSpace(os.Getenv("PUBLIC_PORTAL_ROOT_DOMAIN")), "."); root != "" {
		return root
	}
	return "myrbw.dev"
}

// guildPortalURL is the address of the guild's own public site ("" when the guild has not claimed a subdomain).
func guildPortalURL(guildID string) string {
	raw, err := database.Must().GuildConfig.Field(guildID, "public_domain")
	if err != nil {
		return ""
	}
	var sub *string
	if json.Unmarshal(raw, &sub) != nil || sub == nil || strings.TrimSpace(*sub) == "" {
		return ""
	}
	return "https://" + strings.ToLower(strings.TrimSpace(*sub)) + "." + portalRootDomain()
}

// activeSeasonSlug is the active season's id and its name as a URL part ("Chapter 3 Season 2" is "chapter-3-season-2"), the
// way the website writes it. ok is false when the season cannot be read.
func activeSeasonSlug(guildID string) (id uint, slug string, ok bool) {
	seasonSlugMu.Lock()
	entry, found := seasonSlugCache[guildID]
	seasonSlugMu.Unlock()
	if found && time.Now().Before(entry.expires) {
		return entry.id, entry.slug, true
	}
	season, err := database.Must().Season.Active(guildID)
	if err != nil || season == nil {
		return 0, "", false
	}
	name := strings.TrimSpace(season.Name)
	if name == "" {
		name = fmt.Sprintf("Season %d", season.Number)
	}
	slug = strings.Trim(nonSlugChars.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		slug = "season"
	}
	seasonSlugMu.Lock()
	seasonSlugCache[guildID] = seasonSlugEntry{id: season.ID, slug: slug, expires: time.Now().Add(matchLinkTTL)}
	seasonSlugMu.Unlock()
	return season.ID, slug, true
}

// MatchURL is the website's page for one game ("" when there is no website to link to).
//
// When the guild has a public site of its own the link is on it: /games/<season name>/<game number> for a game of the active
// season, else /games/<guildNumber> (the game's number among every game the guild has recorded, which never repeats; the site
// redirects it to the season form). A guild without a public site falls back to DASHBOARD_URL (/public/<guild>/games/<guildNumber>)
// when that is set.
func MatchURL(guildID string, seasonID uint, gameNumber, guildNumber int) string {
	if guildID == "" {
		return ""
	}
	if portal := guildPortalURL(guildID); portal != "" {
		if id, slug, ok := activeSeasonSlug(guildID); ok && id == seasonID && gameNumber > 0 {
			return fmt.Sprintf("%s/games/%s/%d", portal, slug, gameNumber)
		}
		if guildNumber > 0 {
			return fmt.Sprintf("%s/games/%d", portal, guildNumber)
		}
		return ""
	}
	base := dashboardBase()
	if base == "" || guildNumber < 1 {
		return ""
	}
	return fmt.Sprintf("%s/public/%s/games/%d", base, guildID, guildNumber)
}

// MatchLink is label as a Markdown link (with no link preview) to the game's match page, or just the label when there is no website address.
func MatchLink(guildID string, seasonID uint, gameNumber, guildNumber int, label string) string {
	if url := MatchURL(guildID, seasonID, gameNumber, guildNumber); url != "" {
		return "[" + label + "](<" + url + ">)" // <> keeps Discord from adding a link preview under the message
	}
	return label
}
