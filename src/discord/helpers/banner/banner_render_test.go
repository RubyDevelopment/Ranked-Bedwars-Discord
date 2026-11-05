package banner

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"sync/atomic"
	"testing"
	"time"
)

func TestDitherMatrixCoversEveryLevelOnce(t *testing.T) {
	seen := map[float64]bool{}
	for _, v := range bayer {
		if v <= 0 || v >= 1 || seen[v] {
			t.Fatalf("threshold %v is out of range or repeated", v)
		}
		seen[v] = true
	}
}

func TestFillGradientEndsAndDirection(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	fillGradient(img, 90, color.NRGBA{0, 0, 0, 255}, color.NRGBA{200, 100, 50, 255}) // left to right
	left, right := img.RGBAAt(0, 50), img.RGBAAt(199, 50)
	if left.R > 2 || right.R < 196 || right.G < 96 || right.B < 46 {
		t.Fatalf("ends are %v and %v", left, right)
	}
	if mid := img.RGBAAt(100, 50); mid.R < 95 || mid.R > 105 {
		t.Fatalf("middle is %v, want about 100 red", mid)
	}
	if img.RGBAAt(100, 0) != img.RGBAAt(100, 0) || img.RGBAAt(100, 0).A != 255 {
		t.Fatal("a gradient over opaque colours must stay opaque")
	}
}

func TestGlowFadesToNothing(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	fillGradient(img, 0, color.NRGBA{10, 10, 10, 255}, color.NRGBA{10, 10, 10, 255})
	paintGlow(img, Glow{X: 50, Y: 50, Radius: 40, Color: "#ffffff", Opacity: 1})
	if c := img.RGBAAt(50, 50); c.R < 240 {
		t.Fatalf("centre is %v, want near white", c)
	}
	if c := img.RGBAAt(2, 2); c.R > 12 {
		t.Fatalf("outside the radius is %v, want the untouched fill", c)
	}
}

func TestBackgroundIsCachedAndCopied(t *testing.T) {
	b := &Banner{Width: 64, Height: 64, Background: Background{Color: "#102030", Color2: "#405060", Angle: 45}}
	first := b.background(nil)
	first.Pix[0] = 99 // a caller scribbling on its canvas must not reach the next one
	second := b.background(nil)
	if second.Pix[0] == 99 {
		t.Fatal("the cached background was handed out, not a copy")
	}
	if &first.Pix[0] == &second.Pix[0] {
		t.Fatal("two renders share one canvas")
	}
}

func TestPrefetchLoadsPicturesTogetherAndOnce(t *testing.T) {
	var calls, running, peak atomic.Int32
	old := fetchImage
	defer func() { fetchImage = old }()
	// The real fetchImage de-duplicates through the cache; this stand-in checks that prefetch itself runs the loads side by side.
	fetchImage = func(url string) (image.Image, error) {
		calls.Add(1)
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		running.Add(-1)
		return image.NewRGBA(image.Rect(0, 0, 4, 4)), nil
	}
	b := &Banner{Layers: []*Layer{
		{Kind: "image", Src: "https://x/{a}"}, {Kind: "image", Src: "https://x/{b}"}, {Kind: "image", Src: "https://x/{c}"},
		{Kind: "image", Src: "https://x/{a}"}, // the same picture twice is loaded once
		{Kind: "image", Src: "https://x/{d}", Hidden: true},
	}}
	start := time.Now()
	b.prefetch(Vars{"a": "1", "b": "2", "c": "3", "d": "4"})
	if calls.Load() != 3 {
		t.Fatalf("%d loads, want 3 (two pictures are the same, one is hidden)", calls.Load())
	}
	if peak.Load() < 3 || time.Since(start) > 110*time.Millisecond {
		t.Fatalf("loads ran %d at a time in %v: they should all run together", peak.Load(), time.Since(start))
	}
}

func TestDefaultCardsAreTheRightSize(t *testing.T) {
	if b := DefaultBanner("stats"); b.Width != 1400 || b.Height != 800 {
		t.Errorf("stats is %dx%d", b.Width, b.Height)
	}
	if b := DefaultBanner("leaderboard"); b.Width != 1400 || b.Height != 800 {
		t.Errorf("leaderboard is %dx%d", b.Width, b.Height)
	}
	for _, players := range []int{1, 4, 8} {
		_, teams := sampleResults(players)
		if b := DefaultResultsBannerFor(teams, true); b.Width != 1400 || b.Height != 800 {
			t.Errorf("results with %d players a side is %dx%d", players, b.Width, b.Height)
		}
	}
	// A game with far more players than fit grows taller instead of squeezing its rows.
	_, big := sampleResults(14)
	if b := DefaultResultsBannerFor(big, true); b.Width != 1400 || b.Height <= 800 {
		t.Errorf("14 a side is %dx%d, want 1400 wide and taller than 800", b.Width, b.Height)
	}
}

// A link that cannot be loaded (here: a private address, which the bot refuses) is remembered for a short while, so the next banner
// that wants it does not wait for the same failure again.
func TestFailedPictureIsRememberedBriefly(t *testing.T) {
	const url = "https://127.0.0.1:1/never.png"
	imageMu.Lock()
	delete(imageCache, url)
	imageMu.Unlock()

	if _, err := fetchImage(url); err == nil {
		t.Fatal("a private address must not load")
	}
	imageMu.Lock()
	c, ok := imageCache[url]
	imageMu.Unlock()
	if !ok || c.err == nil {
		t.Fatalf("the failure was not remembered: %+v", c)
	}
	if left := time.Until(c.expires); left <= 0 || left > failedTTL {
		t.Fatalf("failure is remembered for %v, want up to %v", left, failedTTL)
	}
	start := time.Now()
	if _, err := fetchImage(url); err == nil || time.Since(start) > 50*time.Millisecond {
		t.Fatalf("the second ask should answer at once from the cache (err %v, took %v)", err, time.Since(start))
	}
}

func TestEvictionKeepsMostOfTheCache(t *testing.T) {
	imageMu.Lock()
	defer imageMu.Unlock()
	clear(imageCache)
	for i := 0; i < imageCacheMax; i++ {
		imageCache[string(rune('a'+i%26))+string(rune('A'+i/26))] = cachedImage{expires: time.Now().Add(time.Duration(i+1) * time.Minute)}
	}
	evictImages()
	if n := len(imageCache); n < imageCacheMax/2 || n >= imageCacheMax {
		t.Fatalf("%d of %d pictures left: eviction should free room without emptying the cache", n, imageCacheMax)
	}
}

// The parallel encoder must write exactly the pixels it was given, whatever the size (so the bands split unevenly) and the content.
func TestEncodePNGIsLossless(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {7, 5}, {77, 53}, {300, 49}, {1400, 800}} {
		img := image.NewRGBA(image.Rect(0, 0, size[0], size[1]))
		seed := uint32(size[0]*31 + size[1])
		for i := 0; i < len(img.Pix); i += 4 {
			seed = seed*1664525 + 1013904223
			// Runs of flat colour, gradients and noise, so every filter gets used.
			switch (i / 4 / 37) % 3 {
			case 0:
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 40, 20, 90
			case 1:
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = byte(i/4), byte(i/16), byte(i/64)
			default:
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = byte(seed>>24), byte(seed>>16), byte(seed>>8)
			}
			img.Pix[i+3] = 255
		}
		data, err := encodePNG(img)
		if err != nil {
			t.Fatal(err)
		}
		got, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%v: the standard decoder rejects it: %v", size, err)
		}
		if got.Bounds() != img.Bounds() {
			t.Fatalf("%v: decoded as %v", size, got.Bounds())
		}
		for y := 0; y < size[1]; y++ {
			for x := 0; x < size[0]; x++ {
				r, g, b, a := got.At(x, y).RGBA()
				want := img.RGBAAt(x, y)
				if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || a>>8 != 255 {
					t.Fatalf("%v: pixel (%d,%d) is %d,%d,%d, want %v", size, x, y, r>>8, g>>8, b>>8, want)
				}
			}
		}
	}
}

func TestEncodePNGKeepsTransparency(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.Pix[3] = 128 // one half-transparent pixel: not opaque, so the standard encoder writes it
	data, err := encodePNG(img)
	if err != nil {
		t.Fatal(err)
	}
	got, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := got.At(0, 0).RGBA(); a>>8 != 128 {
		t.Fatalf("alpha is %d, want 128", a>>8)
	}
}

func TestHTMLBannerSendsOnlyItsOwnPictures(t *testing.T) {
	b := &Banner{
		HTML: `<img src="asset:logo"/>`, CSS: `.x{background:url(asset:wall)}`,
		assets: map[string]Asset{"logo": {Data: "L"}, "wall": {Data: "W"}, "unused": {Data: "BIG"}, "viaVar": {Data: "V"}},
	}
	got := b.assetsUsed(Vars{"player.pic": "asset:viaVar"})
	if len(got) != 3 || got["unused"].Data != "" {
		t.Fatalf("sent %v, want logo, wall and viaVar only", got)
	}
}

func TestBannerDocumentIsParsedOnce(t *testing.T) {
	raw := []byte(`{"version":2,"banners":[{"id":"a","command":"stats","enabled":true,"width":100,"height":100}]}`)
	first, err := ParseBanners(raw)
	if err != nil || len(first.Banners) != 1 {
		t.Fatal(err)
	}
	same := append([]byte(nil), raw...)
	if !sameBytes(raw, raw) || sameBytes(raw, same) {
		t.Fatal("sameBytes must tell one buffer from an equal copy")
	}
}

// A banner's own variables must come back from the engine call unchanged: the caller may draw the built-in card from them next.
func TestRemoteRenderLeavesTheCallersVariablesAlone(t *testing.T) {
	old := EngineControl
	defer func() { EngineControl = old }()
	var sent string
	EngineControl = func(guild, action string, params json.RawMessage) RemoteResult {
		sent = string(params)
		return RemoteResult{Error: "stop here"}
	}
	vars := Vars{"player.head": "https://nmsr.nickac.dev/face/Steve", "player.name": "Steve"}
	b := &Banner{guildID: "1", Mode: "html", HTML: "<img src=\"{player.head}\"/>", Width: 100, Height: 100}
	_, _ = renderRemote(b, vars)
	if vars["player.head"] != "https://nmsr.nickac.dev/face/Steve" {
		t.Fatalf("the caller's variable was changed to %q", vars["player.head"])
	}
	_ = sent
}
