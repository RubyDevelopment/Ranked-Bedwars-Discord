package banner

import (
	"fmt"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"image"
	"ranked-bedwars/src/database"
	"sort"
	"strings"
	"sync"
	"time"
)

// Pictures a banner draws (heads, bodies, backgrounds) are fetched once, shared between renders, and fetched together: a
// leaderboard of ten heads waits for one round trip, not ten in a row.

const (
	maxImageBytes = 5 << 20
	maxImageSide  = 4096
	imageTTL      = 15 * time.Minute
	failedTTL     = 45 * time.Second // a picture that would not load is not asked for again at once: its slot is a placeholder
	imageCacheMax = 128
)

type cachedImage struct {
	img     image.Image
	err     error
	expires time.Time
}

type fetchCall struct {
	done chan struct{}
	img  image.Image
	err  error
}

var (
	imageMu    sync.Mutex
	imageCache = map[string]cachedImage{}
	inflight   = map[string]*fetchCall{}
)

// evictImages makes room in the cache (the caller holds imageMu): expired pictures go first, then the quarter closest to expiring,
// so one busy moment never empties the whole cache.
func evictImages() {
	if len(imageCache) < imageCacheMax {
		return
	}
	now := time.Now()
	for k, c := range imageCache {
		if now.After(c.expires) {
			delete(imageCache, k)
		}
	}
	if len(imageCache) < imageCacheMax {
		return
	}
	type entry struct {
		key     string
		expires time.Time
	}
	all := make([]entry, 0, len(imageCache))
	for k, c := range imageCache {
		all = append(all, entry{k, c.expires})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].expires.Before(all[j].expires) })
	for _, e := range all[:len(all)/4+1] {
		delete(imageCache, e.key)
	}
}

// fetchImage loads an https image (an avatar, a background): from the cache, by joining a download of the same picture that is
// already running, or by downloading it. It is a variable so tests can serve images without a network.
var fetchImage = func(url string) (image.Image, error) {
	imageMu.Lock()
	if c, ok := imageCache[url]; ok && time.Now().Before(c.expires) {
		imageMu.Unlock()
		return c.img, c.err
	}
	if call, ok := inflight[url]; ok {
		imageMu.Unlock()
		<-call.done
		return call.img, call.err
	}
	call := &fetchCall{done: make(chan struct{})}
	inflight[url] = call
	imageMu.Unlock()

	call.img, call.err = download(url)

	imageMu.Lock()
	delete(inflight, url)
	evictImages()
	ttl := imageTTL
	if call.err != nil {
		ttl = failedTTL
	}
	imageCache[url] = cachedImage{img: call.img, err: call.err, expires: time.Now().Add(ttl)}
	imageMu.Unlock()
	close(call.done)
	return call.img, call.err
}

// prefetch loads every picture the banner is about to draw at the same time. The drawing that follows finds them in the cache.
func (b *Banner) prefetch(vars Vars) {
	seen := map[string]bool{}
	var urls []string
	add := func(src string) {
		if src != "" && !strings.HasPrefix(src, "asset:") && !seen[src] {
			seen[src] = true
			urls = append(urls, src)
		}
	}
	add(vars.fill(b.Background.Image))
	for _, l := range b.Layers {
		if l == nil || l.Kind != "image" {
			continue
		}
		if r := applyRules(l, vars); !r.Hidden {
			add(vars.fill(r.Src))
		}
	}
	if len(urls) < 2 {
		return // one picture is fetched by the drawing itself
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 12)
	for _, u := range urls {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			_, _ = fetchImage(u)
		}()
	}
	wg.Wait()
}

// A leaderboard row's player: the name and picture a card shows. Looking one up takes three calls (the global player, their profile in
// the server, their Discord account) and ten rows make thirty, yet who a player is hardly ever changes: it is kept for a minute, so
// turning the pages of a leaderboard, or asking again, costs nothing.
type rowIdentity struct {
	name, avatar string
	expires      time.Time
}

const rowIdentityTTL = time.Minute

var rowIdentities sync.Map // "guild/player id" -> rowIdentity

func rowPlayer(s *discordgo.Session, guildID string, playerID uint) rowIdentity {
	key := guildID + "/" + sdk.UintToString(playerID)
	if v, ok := rowIdentities.Load(key); ok {
		if who := v.(rowIdentity); time.Now().Before(who.expires) {
			return who
		}
	}
	who := rowIdentity{name: fmt.Sprintf("Player %d", playerID)}
	found := false
	if global, err := database.Must().Player.GetGlobal(sdk.UintToString(playerID)); err == nil {
		found = true
		if view, err := database.Must().Player.Get(guildID, global.SnowflakeID); err == nil {
			who.name = view.Config.Username
		} else {
			found = false
		}
		if m := LookupMember(s, guildID, global.SnowflakeID); m != nil {
			who.avatar = AvatarOf(m.User)
		}
	}
	if found { // a lookup that failed is not remembered: the next ask tries again
		who.expires = time.Now().Add(rowIdentityTTL)
		rowIdentities.Store(key, who)
	}
	return who
}
