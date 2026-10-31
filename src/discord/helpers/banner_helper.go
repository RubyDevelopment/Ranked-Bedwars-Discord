package helpers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"
	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/gofont/gomediumitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/gomonobolditalic"
	"golang.org/x/image/font/gofont/gomonoitalic"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/gofont/gosmallcaps"
	"golang.org/x/image/font/gofont/gosmallcapsitalic"
	_ "golang.org/x/image/webp"

	"ranked-bedwars/src/config"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
)

// Banners draw the image cards (stats, leaderboard, ...) that guilds design in the panel's
// Banner Builder. The panel stores them in the guild config's banner_layouts column as
// {"version": 2, "banners": [...]}; the types below mirror that document (see the panel's
// src/lib/builders/banner.ts). A banner is one style a player can pick for a command: it is a canvas
// plus layers (text, image, shape) whose text and image sources hold {{group.name}} variables, and an
// Access rule saying who may use it.

// Package banners draws the image cards (stats, leaderboard, ...) that guilds design in the panel's
// Banner Builder. The panel stores them in the guild config's banner_layouts column as
// {"version": 2, "banners": [...]}; the types below mirror that document (see the panel's
// src/lib/builders/banner.ts). A banner is one style a player can pick for a command: it is a canvas
// plus layers (text, image, shape) whose text and image sources hold {{group.name}} variables, and an
// Access rule saying who may use it.

// Doc is the whole banner_layouts document.
type Doc struct {
	Version int              `json:"version"`
	Banners []*Banner        `json:"banners"`
	Assets  map[string]Asset `json:"assets"`
}

// Asset is defined in package types (banners.go).
type Asset = types.Asset

// Banner is one style of one command.
type Banner struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Command    string     `json:"command"` // "stats", "leaderboard", "profile" or "custom:<name>"
	Enabled    bool       `json:"enabled"`
	Width      int        `json:"width"`
	Height     int        `json:"height"`
	Background Background `json:"background"`
	Layers     []*Layer   `json:"layers"`
	Access     Access     `json:"access"`

	assets map[string]Asset // set by Parse, for asset: sources
}

// Background is defined in package types (banners.go).
type Background = types.Background

// Shadow is defined in package types (banners.go).
type Shadow = types.Shadow

// Layer is defined in package types (banners.go).
type Layer = types.Layer

// Rule is defined in package types (banners.go).
type Rule = types.Rule

// RuleSet is defined in package types (banners.go).
type RuleSet = types.RuleSet

// Access limits who may use a banner. A player who meets ANY set condition may use it; nothing set means everyone.
type Access struct {
	Roles   []string `json:"roles"`
	Players []string `json:"players"` // Discord user ids
	Perks   []string `json:"perks"`
	Booster bool     `json:"booster"`
}

// Vars are the values behind {{group.name}} placeholders, keyed "group.name".
type Vars map[string]string

// ParseBanners reads a banner_layouts column. An empty or never-saved column is an empty document.
func ParseBanners(raw []byte) (*Doc, error) {
	doc := &Doc{}
	if len(raw) == 0 || string(raw) == "null" {
		return doc, nil
	}
	if err := json.Unmarshal(raw, doc); err != nil {
		return nil, fmt.Errorf("banner layouts: %w", err)
	}
	// Banners saved before the style rework were a free-form map: nothing usable in them.
	if doc.Version != 2 {
		return &Doc{}, nil
	}
	for _, b := range doc.Banners {
		if b != nil {
			b.assets = doc.Assets
		}
	}
	return doc, nil
}

// parseColor reads #rrggbb or #rrggbbaa.
func parseColor(s string) (color.NRGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 && len(s) != 8 {
		return color.NRGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	if len(s) == 6 {
		return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, true
	}
	return color.NRGBA{R: uint8(v >> 24), G: uint8(v >> 16), B: uint8(v >> 8), A: uint8(v)}, true
}

// fill replaces every {path} with its value. The older {{path}} still works and, unlike a single-brace name that
// is not a variable (left as written), becomes empty text when unknown.
func (v Vars) fill(s string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	return varPattern.ReplaceAllStringFunc(s, func(whole string) string {
		m := varPattern.FindStringSubmatch(whole)
		if m[1] != "" {
			return v[m[1]]
		}
		if val, ok := v[m[2]]; ok {
			return val
		}
		return whole
	})
}

func number(s string) (float64, bool) {
	s = strings.NewReplacer(",", "", "%", "", " ", "").Replace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// matches compares like the panel's preview does: numbers as numbers, otherwise text ignoring case.
func matches(op, actual, expected string) bool {
	a, aok := number(actual)
	b, bok := number(expected)
	numeric := aok && bok
	same := func() bool {
		if numeric {
			return a == b
		}
		return strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(expected))
	}
	switch op {
	case "eq":
		return same()
	case "ne":
		return !same()
	case "contains":
		return strings.Contains(strings.ToLower(actual), strings.ToLower(expected))
	case "gt":
		return numeric && a > b
	case "gte":
		return numeric && a >= b
	case "lt":
		return numeric && a < b
	case "lte":
		return numeric && a <= b
	}
	return false
}

// applyRules returns the layer as it looks for this data: every matching rule applied in order.
func applyRules(l *Layer, vars Vars) *Layer {
	if len(l.Rules) == 0 {
		return l
	}
	out := *l
	for _, r := range l.Rules {
		if r.Var == "" || !matches(r.Op, vars[r.Var], r.Value) {
			continue
		}
		if r.Set.Color != nil {
			out.Color = *r.Set.Color
		}
		if r.Set.Color2 != nil {
			out.Color2 = *r.Set.Color2
		}
		if r.Set.Stroke != nil {
			out.Stroke = *r.Set.Stroke
		}
		if r.Set.Opacity != nil {
			out.Opacity = r.Set.Opacity
		}
		if r.Set.Hidden != nil {
			out.Hidden = *r.Set.Hidden
		}
	}
	return &out
}

// Viewer is defined in package types (banners.go).
type Viewer = types.Viewer

// Open reports whether the rule lets everyone in.
func (a Access) Open() bool {
	return len(a.Roles) == 0 && len(a.Players) == 0 && len(a.Perks) == 0 && !a.Booster
}

// Allows reports whether v meets the rule.
func (a Access) Allows(v Viewer) bool {
	if a.Open() {
		return true
	}
	if a.Booster && v.Booster {
		return true
	}
	if slices.Contains(a.Players, v.UserID) {
		return true
	}
	for _, r := range a.Roles {
		if slices.Contains(v.RoleIDs, r) {
			return true
		}
	}
	for _, p := range a.Perks {
		if slices.Contains(v.PerkIDs, p) {
			return true
		}
	}
	return false
}

// Eligible returns the enabled banners of command that v may use, in the order the guild set.
func (d *Doc) Eligible(command string, v Viewer) []*Banner {
	var out []*Banner
	for _, b := range d.Banners {
		if b != nil && b.Enabled && b.Command == command && b.Access.Allows(v) {
			out = append(out, b)
		}
	}
	return out
}

// Choose picks the banner for v: the style they chose (preferred) if they may still use it, otherwise
// the first style they may use. nil means the guild has no usable banner for the command.
func (d *Doc) Choose(command string, v Viewer, preferred string) *Banner {
	list := d.Eligible(command, v)
	for _, b := range list {
		if b.ID == preferred {
			return b
		}
	}
	if len(list) > 0 {
		return list[0]
	}
	return DefaultBanner(command) // a server that designed none gets the built-in card
}

// The preset fonts are compiled into the binary: the Go font family, plus a few open-licence (SIL OFL)
// fonts embedded by the config package (licences alongside). A style a family lacks (bold small caps,
// italic Rajdhani) falls back to the closest one it has.

func bundle(name string) []byte {
	data, err := config.Fonts.ReadFile("fonts/" + name)
	if err != nil {
		panic(err) // a missing embedded file is a build mistake
	}
	return data
}

var fontFiles = map[string][4][]byte{ // regular, bold, italic, bold italic
	"sans":       {goregular.TTF, gobold.TTF, goitalic.TTF, gobolditalic.TTF},
	"medium":     {gomedium.TTF, gobold.TTF, gomediumitalic.TTF, gobolditalic.TTF},
	"mono":       {gomono.TTF, gomonobold.TTF, gomonoitalic.TTF, gomonobolditalic.TTF},
	"smallcaps":  {gosmallcaps.TTF, gosmallcaps.TTF, gosmallcapsitalic.TTF, gosmallcapsitalic.TTF},
	"rajdhani":   {bundle("Rajdhani-Regular.ttf"), bundle("Rajdhani-Bold.ttf"), nil, nil},
	"bebas":      {bundle("BebasNeue-Regular.ttf"), nil, nil, nil},
	"silkscreen": {bundle("Silkscreen-Regular.ttf"), bundle("Silkscreen-Bold.ttf"), nil, nil},
	"pixel":      {bundle("PressStart2P-Regular.ttf"), nil, nil, nil},
	"vt323":      {bundle("VT323-Regular.ttf"), nil, nil, nil},
}

var (
	fontMu    sync.Mutex
	parsed    = map[string]*truetype.Font{}
	faceCache = map[string]font.Face{}
)

// faceFor returns the face of a preset at size px (cached: parsing a font costs more than drawing a banner).
func faceFor(preset string, bold, italic bool, size float64) (font.Face, error) {
	if _, ok := fontFiles[preset]; !ok {
		preset = "sans"
	}
	if size < 1 {
		size = 1
	}
	idx := 0
	if bold {
		idx |= 1
	}
	if italic {
		idx |= 2
	}
	// The closest style the family has: drop italic first, then bold.
	for _, try := range []int{idx, idx &^ 2, idx &^ 3} {
		if fontFiles[preset][try] != nil {
			idx = try
			break
		}
	}
	key := fmt.Sprintf("%s/%d/%.1f", preset, idx, size)

	fontMu.Lock()
	defer fontMu.Unlock()
	if f, ok := faceCache[key]; ok {
		return f, nil
	}
	fileKey := fmt.Sprintf("%s/%d", preset, idx)
	tt, ok := parsed[fileKey]
	if !ok {
		var err error
		if tt, err = truetype.Parse(fontFiles[preset][idx]); err != nil {
			return nil, err
		}
		parsed[fileKey] = tt
	}
	face := truetype.NewFace(tt, &truetype.Options{Size: size, DPI: 72})
	if len(faceCache) > 512 {
		clear(faceCache)
	}
	faceCache[key] = face
	return face, nil
}

const (
	maxImageBytes = 5 << 20
	maxImageSide  = 4096
	imageTTL      = 15 * time.Minute
	imageCacheMax = 128
)

type cachedImage struct {
	img     image.Image
	expires time.Time
}

var (
	imageMu    sync.Mutex
	imageCache = map[string]cachedImage{}
)

// fetchImage loads an https image (an avatar, a background). It is a variable so tests can serve images without a network.
var fetchImage = func(url string) (image.Image, error) {
	imageMu.Lock()
	if c, ok := imageCache[url]; ok && time.Now().Before(c.expires) {
		imageMu.Unlock()
		return c.img, nil
	}
	imageMu.Unlock()

	img, err := download(url)
	if err != nil {
		return nil, err
	}
	imageMu.Lock()
	if len(imageCache) >= imageCacheMax {
		clear(imageCache)
	}
	imageCache[url] = cachedImage{img: img, expires: time.Now().Add(imageTTL)}
	imageMu.Unlock()
	return img, nil
}

// Banner image links are written by guild staff, so the bot only follows https links to public hosts.
var client = &http.Client{
	Timeout: 6 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() || ip.IP.IsUnspecified() || ip.IP.IsMulticast() {
					return nil, errors.New("image host is not public")
				}
			}
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		TLSHandshakeTimeout: 4 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" {
			return errors.New("redirect refused")
		}
		return nil
	},
}

func download(url string) (image.Image, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme != "https" {
		return nil, errors.New("image links must be https")
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image answered %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, errors.New("image is too large")
	}
	cfg, _, err := image.DecodeConfig(bytesReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width > maxImageSide || cfg.Height > maxImageSide {
		return nil, errors.New("image is too large")
	}
	img, _, err := image.Decode(bytesReader(data))
	return img, err
}

// source loads a banner picture: an https link, or `asset:<id>` for a picture uploaded in the builder.
func (b *Banner) source(src string) (image.Image, error) {
	id, ok := strings.CutPrefix(src, "asset:")
	if !ok {
		return fetchImage(src)
	}
	a, found := b.assets[id]
	if !found {
		return nil, errors.New("unknown uploaded picture")
	}
	return decodeDataURL(a.Data)
}

func decodeDataURL(data string) (image.Image, error) {
	sum := sha256.Sum256([]byte(data))
	key := "data:" + hex.EncodeToString(sum[:8])
	imageMu.Lock()
	if c, ok := imageCache[key]; ok {
		imageMu.Unlock()
		return c.img, nil
	}
	imageMu.Unlock()

	_, payload, ok := strings.Cut(data, ",")
	if !ok || len(data) > 900_000 {
		return nil, errors.New("bad picture")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytesReader(raw))
	if err != nil || cfg.Width > maxImageSide || cfg.Height > maxImageSide {
		return nil, errors.New("bad picture")
	}
	img, _, err := image.Decode(bytesReader(raw))
	if err != nil {
		return nil, err
	}
	imageMu.Lock()
	if len(imageCache) >= imageCacheMax {
		clear(imageCache)
	}
	imageCache[key] = cachedImage{img: img, expires: time.Now().Add(24 * time.Hour)}
	imageMu.Unlock()
	return img, nil
}

const maxSide = 2400

// RenderBanner draws banner b with vars filled in and returns it as a PNG. A layer that cannot be drawn (an
// image that will not load) is skipped or shown as a placeholder: one bad link never loses the banner.
func RenderBanner(b *Banner, vars Vars) ([]byte, error) {
	w, h := b.Width, b.Height
	if w < 50 || h < 50 || w > maxSide || h > maxSide {
		return nil, errors.New("banner size is out of range")
	}
	dc := gg.NewContext(w, h)
	drawBackground(dc, b, vars)
	for _, l := range b.Layers {
		if l == nil || l.W < 1 || l.H < 1 || l.W > 6000 || l.H > 6000 {
			continue
		}
		drawLayer(dc, b, l, vars)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, dc.Image()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func nrgba(s string, def color.NRGBA) color.NRGBA {
	if c, ok := parseColor(s); ok {
		return c
	}
	return def
}

// gradient is the CSS linear-gradient(angle, a, b) over a box: 0deg points up, 90deg right.
func gradient(x, y, w, h, angle float64, a, b color.Color) gg.Gradient {
	rad := angle * math.Pi / 180
	dx, dy := math.Sin(rad), -math.Cos(rad)
	half := (math.Abs(w*math.Sin(rad)) + math.Abs(h*math.Cos(rad))) / 2
	cx, cy := x+w/2, y+h/2
	g := gg.NewLinearGradient(cx-dx*half, cy-dy*half, cx+dx*half, cy+dy*half)
	g.AddColorStop(0, a)
	g.AddColorStop(1, b)
	return g
}

func drawBackground(dc *gg.Context, b *Banner, vars Vars) {
	bg := b.Background
	w, h := float64(b.Width), float64(b.Height)
	c1 := nrgba(bg.Color, color.NRGBA{27, 29, 38, 255})
	dc.DrawRectangle(0, 0, w, h)
	if c2, ok := parseColor(bg.Color2); ok && bg.Color2 != "" {
		dc.SetFillStyle(gradient(0, 0, w, h, bg.Angle, c1, c2))
	} else {
		dc.SetColor(c1)
	}
	dc.Fill()
	if bg.Image != "" {
		if img, err := b.source(vars.fill(bg.Image)); err == nil {
			paintImage(dc, img, 0, 0, w, h, bg.Fit, bg.Blur)
		}
	}
	if bg.OverlayOpacity > 0 {
		c := nrgba(bg.Overlay, color.NRGBA{0, 0, 0, 255})
		c.A = uint8(float64(c.A) * math.Min(1, bg.OverlayOpacity))
		dc.SetColor(c)
		dc.DrawRectangle(0, 0, w, h)
		dc.Fill()
	}
}

// paintImage draws img into the box (x, y, w, h) like CSS object-fit, resampled once for a clean edge.
func paintImage(dc *gg.Context, img image.Image, x, y, w, h float64, fit string, blur float64) {
	b := img.Bounds()
	iw, ih := float64(b.Dx()), float64(b.Dy())
	if iw == 0 || ih == 0 {
		return
	}
	s := math.Max(w/iw, h/ih) // cover
	if fit == "contain" {
		s = math.Min(w/iw, h/ih)
	}
	tw, th := int(math.Round(iw*s)), int(math.Round(ih*s))
	if tw < 1 || th < 1 {
		return
	}
	scaled := image.NewRGBA(image.Rect(0, 0, tw, th))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), img, b, xdraw.Over, nil)
	if blur > 0 {
		blurImage(scaled, blur)
	}
	dc.DrawImage(scaled, int(math.Round(x+(w-float64(tw))/2)), int(math.Round(y+(h-float64(th))/2)))
}

// drawLayer paints a layer on its own canvas (with room for its shadow), then places it on the banner with
// its opacity and rotation, so shadows and fades behave the same for text, images and blocks.
func drawLayer(dc *gg.Context, b *Banner, src *Layer, vars Vars) {
	l := applyRules(src, vars)
	if l.Hidden {
		return
	}
	pad := 0
	if l.Shadow.On {
		pad = int(math.Ceil(l.Shadow.Blur*2+math.Max(math.Abs(l.Shadow.X), math.Abs(l.Shadow.Y)))) + 2
	}
	lw, lh := int(math.Ceil(l.W))+2*pad, int(math.Ceil(l.H))+2*pad
	lc := gg.NewContext(lw, lh)
	box := rect{float64(pad), float64(pad), l.W, l.H}
	switch l.Kind {
	case "text":
		drawText(lc, l, box, vars)
	case "image":
		drawImageLayer(lc, b, l, box, vars)
	case "shape":
		drawShape(lc, l, box)
	default:
		return
	}
	img := lc.Image().(*image.RGBA)
	if l.Shadow.On {
		img = withShadow(img, l.Shadow)
	}
	if l.Opacity != nil {
		switch op := math.Min(1, math.Max(0, *l.Opacity)); {
		case op == 0:
			return
		case op < 1:
			fade(img, op)
		}
	}

	x, y := int(math.Round(l.X))-pad, int(math.Round(l.Y))-pad
	if l.Rotation == 0 {
		draw.Draw(dc.Image().(*image.RGBA), image.Rect(x, y, x+lw, y+lh), img, image.Point{}, draw.Over)
		return
	}
	dc.Push()
	dc.RotateAbout(gg.Radians(l.Rotation), l.X+l.W/2, l.Y+l.H/2)
	dc.DrawImage(img, x, y)
	dc.Pop()
}

type rect struct{ x, y, w, h float64 }

func shapePath(dc *gg.Context, ellipse bool, r rect, radius float64) {
	switch {
	case ellipse:
		dc.DrawEllipse(r.x+r.w/2, r.y+r.h/2, r.w/2, r.h/2)
	case radius > 0:
		dc.DrawRoundedRectangle(r.x, r.y, r.w, r.h, math.Min(radius, math.Min(r.w, r.h)/2))
	default:
		dc.DrawRectangle(r.x, r.y, r.w, r.h)
	}
}

// border strokes inside the box, like a CSS border on a border-box element.
func border(dc *gg.Context, l *Layer, r rect) {
	if l.StrokeWidth <= 0 || l.Stroke == "" {
		return
	}
	c, ok := parseColor(l.Stroke)
	if !ok {
		return
	}
	sw := l.StrokeWidth
	in := rect{r.x + sw/2, r.y + sw/2, r.w - sw, r.h - sw}
	if in.w <= 0 || in.h <= 0 {
		return
	}
	shapePath(dc, l.Shape == "ellipse", in, math.Max(0, l.Radius-sw/2))
	dc.SetColor(c)
	dc.SetLineWidth(sw)
	dc.Stroke()
}

func drawShape(dc *gg.Context, l *Layer, r rect) {
	c1 := nrgba(l.Color, color.NRGBA{39, 42, 55, 255})
	shapePath(dc, l.Shape == "ellipse", r, l.Radius)
	if c2, ok := parseColor(l.Color2); ok && l.Color2 != "" {
		dc.SetFillStyle(gradient(r.x, r.y, r.w, r.h, l.Angle, c1, c2))
	} else {
		dc.SetColor(c1)
	}
	dc.Fill()
	border(dc, l, r)
}

func drawImageLayer(dc *gg.Context, b *Banner, l *Layer, r rect, vars Vars) {
	img, err := b.source(vars.fill(l.Src))
	if err != nil {
		shapePath(dc, false, r, l.Radius)
		dc.SetColor(color.NRGBA{128, 128, 128, 64})
		dc.Fill()
		return
	}
	shapePath(dc, false, r, l.Radius)
	dc.Clip()
	paintImage(dc, img, r.x, r.y, r.w, r.h, l.Fit, l.Blur)
	dc.ResetClip()
	border(dc, l, r)
}

// ── Text ────────────────────────────────────────────────────────────────────

const lineHeight = 1.15

func drawText(dc *gg.Context, l *Layer, r rect, vars Vars) {
	face, err := faceFor(l.Font, l.Bold, l.Italic, l.Size)
	if err != nil {
		return
	}
	dc.SetFontFace(face)
	dc.SetColor(nrgba(l.Color, color.NRGBA{255, 255, 255, 255}))
	m := face.Metrics()
	ascent, descent := float64(m.Ascent)/64, float64(m.Descent)/64
	step := l.Size * lineHeight
	lines := wrap(face, vars.fill(l.Text), r.w, l.LetterSpacing)
	total := step * float64(len(lines))

	top := 0.0
	switch l.VAlign {
	case "middle":
		top = (r.h - total) / 2
	case "bottom":
		top = r.h - total
	}
	dc.DrawRectangle(r.x, r.y, r.w, r.h)
	dc.Clip()
	for i, line := range lines {
		width := measure(face, line, l.LetterSpacing)
		x := r.x
		switch l.Align {
		case "center":
			x += (r.w - width) / 2
		case "right":
			x += r.w - width
		}
		// The line box is `step` tall; the glyphs sit centred in it, like CSS line-height.
		base := r.y + top + float64(i)*step + (step-(ascent+descent))/2 + ascent
		drawString(dc, face, line, x, base, l.LetterSpacing)
	}
	dc.ResetClip()
}

func measure(face font.Face, s string, spacing float64) float64 {
	w := float64(font.MeasureString(face, s)) / 64
	if n := len([]rune(s)); n > 1 {
		w += spacing * float64(n-1)
	}
	return w
}

func drawString(dc *gg.Context, face font.Face, s string, x, base, spacing float64) {
	if spacing == 0 {
		dc.DrawString(s, x, base)
		return
	}
	for _, r := range s {
		dc.DrawString(string(r), x, base)
		x += float64(font.MeasureString(face, string(r)))/64 + spacing
	}
}

// wrap breaks text into lines no wider than w: at newlines, then at spaces, then inside a word too long for a line.
func wrap(face font.Face, text string, w, spacing float64) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		cur := ""
		for _, word := range strings.Fields(para) {
			candidate := word
			if cur != "" {
				candidate = cur + " " + word
			}
			if measure(face, candidate, spacing) <= w {
				cur = candidate
				continue
			}
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			if measure(face, word, spacing) <= w {
				cur = word
				continue
			}
			part := ""
			for _, r := range word {
				if part != "" && measure(face, part+string(r), spacing) > w {
					lines = append(lines, part)
					part = ""
				}
				part += string(r)
			}
			cur = part
		}
		lines = append(lines, cur)
	}
	return lines
}

// ── Shadow and fade ─────────────────────────────────────────────────────────

// withShadow returns img with a blurred, offset, tinted copy of its silhouette behind it.
func withShadow(img *image.RGBA, s Shadow) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	alpha := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			alpha[y*w+x] = float32(img.Pix[y*img.Stride+x*4+3]) / 255
		}
	}
	// CSS drop-shadow blur is 2 sigma; three box blurs stand in for the gaussian.
	if radius := int(s.Blur / 2 * 1.2); radius > 0 {
		tmp := make([]float32, len(alpha))
		for i := 0; i < 3; i++ {
			boxBlur(alpha, tmp, w, h, radius, true)
			boxBlur(tmp, alpha, w, h, radius, false)
		}
	}
	c := nrgba(s.Color, color.NRGBA{0, 0, 0, 153})
	out := image.NewRGBA(b)
	dx, dy := int(math.Round(s.X)), int(math.Round(s.Y))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx, sy := x-dx, y-dy
			if sx < 0 || sy < 0 || sx >= w || sy >= h {
				continue
			}
			a := alpha[sy*w+sx] * float32(c.A) / 255
			o := y*out.Stride + x*4
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(float32(c.R)*a), uint8(float32(c.G)*a), uint8(float32(c.B)*a), uint8(a*255)
		}
	}
	draw.Draw(out, b, img, b.Min, draw.Over)
	return out
}

// boxBlur averages each value with its radius neighbours along a row (horizontal) or a column.
func boxBlur(src, dst []float32, w, h, r int, horizontal bool) {
	n, lines, stride, step := w, h, w, 1
	if !horizontal {
		n, lines, stride, step = h, w, 1, w
	}
	for l := 0; l < lines; l++ {
		base := l * stride
		if !horizontal {
			base = l
		}
		var sum float32
		for i := -r; i <= r; i++ {
			if i >= 0 && i < n {
				sum += src[base+i*step]
			}
		}
		for i := 0; i < n; i++ {
			dst[base+i*step] = sum / float32(2*r+1)
			if add := i + r + 1; add < n {
				sum += src[base+add*step]
			}
			if del := i - r; del >= 0 {
				sum -= src[base+del*step]
			}
		}
	}
}

// fade scales a premultiplied image by opacity.
func fade(img *image.RGBA, opacity float64) {
	k := uint32(opacity * 256)
	for i := range img.Pix {
		img.Pix[i] = uint8(uint32(img.Pix[i]) * k >> 8)
	}
}

// blurImage blurs img in place (three box blurs approximate a gaussian of the CSS blur radius).
func blurImage(img *image.RGBA, blur float64) {
	radius := int(blur / 2 * 1.2)
	if radius < 1 {
		return
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	a, tmp := make([]float32, w*h), make([]float32, w*h)
	for ch := 0; ch < 4; ch++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				a[y*w+x] = float32(img.Pix[y*img.Stride+x*4+ch])
			}
		}
		for i := 0; i < 3; i++ {
			boxBlur(a, tmp, w, h, radius, true)
			boxBlur(tmp, a, w, h, radius, false)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.Pix[y*img.Stride+x*4+ch] = uint8(a[y*w+x] + 0.5)
			}
		}
	}
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// Shared plumbing for the commands that answer with a banner from the panel's Banner Builder
// (/stats, /leaderboard): who the viewer is, which styles they may use, and which one they picked.

// LoadBanners reads the guild's banner styles. An unreadable document counts as none: the commands fall back to their embeds.
func LoadBanners(guildID string) *Doc {
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		return &Doc{}
	}
	doc, err := ParseBanners([]byte(cfg.BannerLayouts))
	if err != nil {
		return &Doc{}
	}
	return doc
}

// BannerViewer is who a banner is chosen for. The bot has no perk system yet, so no perks are passed.
func BannerViewer(userID string, member *discordgo.Member) Viewer {
	v := Viewer{UserID: userID}
	if member != nil {
		v.RoleIDs = member.Roles
		v.Booster = member.PremiumSince != nil
	}
	return v
}

// LookupMember finds a member in the state cache, then asks Discord. nil when they have left.
func LookupMember(s *discordgo.Session, guildID, userID string) *discordgo.Member {
	if m, err := s.State.Member(guildID, userID); err == nil && m != nil {
		return m
	}
	m, err := s.GuildMember(guildID, userID)
	if err != nil {
		return nil
	}
	return m
}

func AvatarOf(u *discordgo.User) string {
	if u == nil {
		return ""
	}
	return u.AvatarURL("256")
}

// ChosenBanner is the style the player picked for command, or "".
func ChosenBanner(cfg *models.PlayerConfigsModel, command string) string {
	if cfg == nil || len(cfg.BannerChoices) == 0 {
		return ""
	}
	var choices map[string]string
	if json.Unmarshal([]byte(cfg.BannerChoices), &choices) != nil {
		return ""
	}
	return choices[command]
}

// RememberBanner stores the player's pick for command next to their other picks.
func RememberBanner(guildID, userID string, cfg *models.PlayerConfigsModel, command, bannerID string) error {
	choices := map[string]string{}
	if cfg != nil && len(cfg.BannerChoices) > 0 {
		_ = json.Unmarshal([]byte(cfg.BannerChoices), &choices)
	}
	if choices[command] == bannerID {
		return nil
	}
	choices[command] = bannerID
	_, err := database.Must().Player.UpdateConfig(userID, guildID, userID, map[string]any{"banner_choices": choices})
	return err
}

// StyleOption is the slash option both commands share.
func StyleOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Name:         "style",
		Description:  "Pick the banner style to use for yourself (remembered for next time)",
		Type:         discordgo.ApplicationCommandOptionString,
		Required:     false,
		Autocomplete: true,
	}
}

// StyleAutocomplete answers with the styles of command the invoker may use.
func StyleAutocomplete(command string) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		typed := ""
		for _, opt := range i.ApplicationCommandData().Options {
			if opt.Focused {
				typed = strings.ToLower(opt.StringValue())
			}
		}
		var choices []*discordgo.ApplicationCommandOptionChoice
		for _, b := range LoadBanners(i.GuildID).Eligible(command, BannerViewer(InteractionUserID(i), i.Member)) {
			if typed == "" || strings.Contains(strings.ToLower(b.Name), typed) {
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: truncateRunes(b.Name, 100), Value: b.ID})
			}
			if len(choices) == 25 {
				break
			}
		}
		_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{Choices: choices},
		})
	}
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// GuildVars are the {{guild.*}} and {{user.*}} values every banner may use.
func GuildVars(s *discordgo.Session, guildID string, invoker *discordgo.User) Vars {
	v := Vars{}
	if g, err := s.State.Guild(guildID); err == nil && g != nil {
		v["guild.name"], v["guild.icon"], v["guild.member_count"] = g.Name, g.IconURL("256"), fmt.Sprint(g.MemberCount)
	}
	if invoker != nil {
		v["user.name"], v["user.mention"], v["user.avatar"] = invoker.Username, invoker.Mention(), AvatarOf(invoker)
	}
	return v
}

// Ratio is a/b to two places; with nothing to divide by it is a itself, as the game stats pages show it.
func Ratio(a, b int) string {
	if b == 0 {
		return fmt.Sprintf("%.2f", float64(a))
	}
	return fmt.Sprintf("%.2f", float64(a)/float64(b))
}

func DateOf(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "never"
	}
	return t.Format("2006-01-02")
}

// Minecraft heads and bodies come from mc-heads.net, by the player's registered username.
func HeadURL(name string) string {
	return "https://mc-heads.net/avatar/" + url.PathEscape(name)
}
func BodyURL(name string) string { return "https://mc-heads.net/body/" + url.PathEscape(name) }

// EntryVars are the fields of one leaderboard row ("lb.3.") or of the invoker's own row ("me."), as the
// panel's Banner Builder lists them.
func EntryVars(vars Vars, prefix string, position int, name, avatar string, st *models.PlayerStatsModel) {
	d := func(n int) string { return fmt.Sprint(n) }
	vars[prefix+"position"], vars[prefix+"name"], vars[prefix+"rank"] = d(position), name, st.Rank
	vars[prefix+"elo"], vars[prefix+"peak"] = d(st.Elo), d(st.HighestElo)
	vars[prefix+"wins"], vars[prefix+"losses"], vars[prefix+"games"] = d(st.Wins), d(st.Losses), d(st.GamesPlayed)
	vars[prefix+"kills"], vars[prefix+"deaths"], vars[prefix+"mvps"] = d(st.Kills), d(st.Deaths), d(st.MvpCount)
	vars[prefix+"wlr"], vars[prefix+"kdr"] = Ratio(st.Wins, st.Losses), Ratio(st.Kills, st.Deaths)
	vars[prefix+"avatar"], vars[prefix+"head"], vars[prefix+"body"] = avatar, HeadURL(name), BodyURL(name)
}

// StatsVars are the {{player.*}}, {{stats.*}} and {{season.*}} values of the stats banner.
func StatsVars(target *discordgo.User, config *models.PlayerConfigsModel, st *models.PlayerStatsModel, position int, season string, seasonID uint) Vars {
	games := st.Wins + st.Losses
	winRate, kdr := 0.0, 0.0
	if games > 0 {
		winRate = float64(st.Wins) / float64(games) * 100
	}
	if st.Deaths > 0 {
		kdr = float64(st.Kills) / float64(st.Deaths)
	}
	d := func(n int) string { return fmt.Sprint(n) }
	return Vars{
		"player.name": config.Username, "player.id": target.ID, "player.mention": target.Mention(), "player.avatar": AvatarOf(target),
		"player.head": HeadURL(config.Username), "player.body": BodyURL(config.Username), "player.joined": config.CreatedAt.Format("2006-01-02"),
		"stats.wlr": Ratio(st.Wins, st.Losses), "stats.fkdr": Ratio(st.FinalKills, st.Deaths), "stats.bdr": Ratio(st.BedsDestroyed, st.BedsLost), "stats.last_played": DateOf(st.LastPlayedAt),
		"stats.rank": st.Rank, "stats.position": d(position), "stats.elo": d(st.Elo), "stats.highest_elo": d(st.HighestElo),
		"stats.games_played": d(st.GamesPlayed), "stats.wins": d(st.Wins), "stats.losses": d(st.Losses), "stats.win_rate": fmt.Sprintf("%.0f%%", winRate),
		"stats.kills": d(st.Kills), "stats.final_kills": d(st.FinalKills), "stats.first_bloods": d(st.FirstBloods), "stats.deaths": d(st.Deaths),
		"stats.assists": d(st.Assists), "stats.kdr": fmt.Sprintf("%.2f", kdr), "stats.beds_destroyed": d(st.BedsDestroyed), "stats.beds_lost": d(st.BedsLost),
		"stats.mvp_count": d(st.MvpCount), "stats.win_streak": d(st.WinStreak), "stats.highest_win_streak": d(st.HighestWinStreak), "stats.loss_streak": d(st.LossStreak),
		"season.name": season, "season.id": d(int(seasonID)),
	}
}

// LeaderboardRowVars fills {{lb.N.*}} for each row. A row's player is looked up in parallel (their name
// and Discord account), so ten rows cost about one round trip.
func LeaderboardRowVars(s *discordgo.Session, guildID string, rows []sdk.LeaderboardEntry, vars Vars) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	for idx := range rows {
		wg.Add(1)
		go func(n int, row sdk.LeaderboardEntry) {
			defer wg.Done()
			name, avatar := fmt.Sprintf("Player %d", row.PlayerID), ""
			if global, err := database.Must().Player.GetGlobal(sdk.UintToString(row.PlayerID)); err == nil {
				if view, err := database.Must().Player.Get(guildID, global.SnowflakeID); err == nil {
					name = view.Config.Username
				}
				if m := LookupMember(s, guildID, global.SnowflakeID); m != nil {
					avatar = AvatarOf(m.User)
				}
			}
			mu.Lock()
			EntryVars(vars, fmt.Sprintf("lb.%d.", n), row.Position, name, avatar, &row.PlayerStatsModel)
			mu.Unlock()
		}(idx+1, rows[idx])
	}
	wg.Wait()
}
