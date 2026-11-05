package banner

import (
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"sync"

	"github.com/fogleman/gg"

	"ranked-bedwars/src/types"
)

// ── Background ──────────────────────────────────────────────────────────────

// bayer is an 8x8 ordered-dither matrix, each cell a threshold in (0, 1). Random noise would dither just as well but PNG cannot
// compress it: the banner would be about twice the size for no visible gain.
var bayer = func() (m [64]float64) {
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			v, xy := 0, x^y
			for i := 2; i >= 0; i-- {
				v = v<<2 | (y>>i&1)<<1 | (xy >> i & 1)
			}
			m[y<<3|x] = (float64(v) + 0.5) / 64
		}
	}
	return m
}()

// ditherAt is the dither threshold of a pixel, in (0, 1).
func ditherAt(x, y int) float64 { return bayer[(y&7)<<3|x&7] }

// fillGradient paints the CSS linear-gradient(angle, a, b) over the whole image in one pass over the pixels. The colours are dithered
// with a fine noise: a dark gradient has only a few levels to move through, and without it each level is a visible band.
func fillGradient(img *image.RGBA, angle float64, a, b color.NRGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	rad := angle * math.Pi / 180
	dx, dy := math.Sin(rad), -math.Cos(rad)
	half := (math.Abs(float64(w)*math.Sin(rad)) + math.Abs(float64(h)*math.Cos(rad))) / 2
	x0, y0 := float64(w)/2-dx*half, float64(h)/2-dy*half
	inv := 1 / (2 * half)
	pre := func(c color.NRGBA) [4]float64 {
		k := float64(c.A) / 255
		return [4]float64{float64(c.R) * k, float64(c.G) * k, float64(c.B) * k, float64(c.A)}
	}
	pa, pb := pre(a), pre(b)
	var step [4]float64
	for c := range step {
		step[c] = pb[c] - pa[c]
	}
	tx := dx * inv
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		tRow := ((0-x0)*dx + (float64(y)-y0)*dy) * inv
		for x := 0; x < w; x++ {
			t := math.Min(1, math.Max(0, tRow+float64(x)*tx))
			u := ditherAt(x, y)
			o := x * 4
			for c := 0; c < 4; c++ {
				row[o+c] = uint8(math.Min(255, pa[c]+step[c]*t+u))
			}
		}
	}
}

// bgCache keeps the canvases of recent backgrounds (a gradient, a blurred picture): they are the same on every render of a banner.
var bgCache = struct {
	mu    sync.Mutex
	order []string // oldest first
	by    map[string]*image.RGBA
	bytes int
}{by: map[string]*image.RGBA{}}

const bgCacheBytes = 96 << 20

// background returns a fresh canvas of the banner's size with its background painted: copied from the cache when it was drawn before.
func (b *Banner) background(vars Vars) *image.RGBA {
	bg := b.Background
	src := vars.fill(bg.Image)
	keyBG := bg
	keyBG.Image = src
	key := fmt.Sprintf("%dx%d|%+v", b.Width, b.Height, keyBG)
	if id, ok := strings.CutPrefix(src, "asset:"); ok { // the picture itself, not its name, decides what was drawn
		key += fmt.Sprintf("|%x", sha256.Sum256([]byte(b.assets[id].Data)))
	}

	bgCache.mu.Lock()
	cached := bgCache.by[key]
	bgCache.mu.Unlock()
	if cached != nil {
		out := image.NewRGBA(cached.Rect)
		copy(out.Pix, cached.Pix)
		return out
	}

	img, complete := b.paintBackground(bg, src)
	if complete {
		bgCache.mu.Lock()
		if _, ok := bgCache.by[key]; !ok {
			size := len(img.Pix)
			for bgCache.bytes+size > bgCacheBytes && len(bgCache.order) > 0 {
				old := bgCache.order[0]
				bgCache.order = bgCache.order[1:]
				bgCache.bytes -= len(bgCache.by[old].Pix)
				delete(bgCache.by, old)
			}
			keep := image.NewRGBA(img.Rect)
			copy(keep.Pix, img.Pix)
			bgCache.by[key], bgCache.order, bgCache.bytes = keep, append(bgCache.order, key), bgCache.bytes+size
		}
		bgCache.mu.Unlock()
	}
	return img
}

// paintBackground draws the canvas fill, its picture and its tint. complete is false when the picture could not be loaded, so the
// canvas is not cached and the next render tries again.
func (b *Banner) paintBackground(bg Background, src string) (img *image.RGBA, complete bool) {
	img = image.NewRGBA(image.Rect(0, 0, b.Width, b.Height))
	complete = true
	c1 := nrgba(bg.Color, color.NRGBA{27, 29, 38, 255})
	if c2, ok := parseColor(bg.Color2); ok && bg.Color2 != "" {
		fillGradient(img, bg.Angle, c1, c2)
	} else {
		draw.Draw(img, img.Bounds(), &image.Uniform{c1}, image.Point{}, draw.Src)
	}
	for _, g := range bg.Glows {
		paintGlow(img, g)
	}
	if src != "" {
		if pic, err := b.source(src); err == nil {
			paintImage(img, nil, pic, 0, 0, float64(b.Width), float64(b.Height), bg.Fit, bg.Blur)
		} else {
			complete = false
		}
	}
	if bg.OverlayOpacity > 0 {
		c := nrgba(bg.Overlay, color.NRGBA{0, 0, 0, 255})
		c.A = uint8(float64(c.A) * math.Min(1, bg.OverlayOpacity))
		draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Over)
	}
	return img, complete
}

// ── PNG ─────────────────────────────────────────────────────────────────────

type pngBuffers struct{ pool sync.Pool }

func (p *pngBuffers) Get() *png.EncoderBuffer {
	b, _ := p.pool.Get().(*png.EncoderBuffer)
	return b
}

func (p *pngBuffers) Put(b *png.EncoderBuffer) { p.pool.Put(b) }

// pngEncoder reuses its compression buffers between renders. The picture is lossless at any level; the level only trades size for time.
var pngEncoder = png.Encoder{CompressionLevel: png.DefaultCompression, BufferPool: &pngBuffers{}}

// Glow is defined in package types (banners.go).
type Glow = types.Glow

// paintGlow lights the image from a point: the colour at the centre, fading smoothly to nothing at the radius.
func paintGlow(img *image.RGBA, g Glow) {
	if g.Radius <= 0 || g.Opacity <= 0 {
		return
	}
	c := nrgba(g.Color, color.NRGBA{139, 92, 246, 255})
	peak := math.Min(1, g.Opacity) * float64(c.A) / 255
	col := [3]float64{float64(c.R), float64(c.G), float64(c.B)}
	b := img.Bounds()
	x0, x1 := max(b.Min.X, int(math.Floor(g.X-g.Radius))), min(b.Max.X, int(math.Ceil(g.X+g.Radius)))
	y0, y1 := max(b.Min.Y, int(math.Floor(g.Y-g.Radius))), min(b.Max.Y, int(math.Ceil(g.Y+g.Radius)))
	for y := y0; y < y1; y++ {
		dy := float64(y) - g.Y
		for x := x0; x < x1; x++ {
			dx := float64(x) - g.X
			t := math.Sqrt(dx*dx+dy*dy) / g.Radius
			if t >= 1 {
				continue
			}
			f := 1 - t
			a := peak * f * f // a soft, gaussian-like falloff
			u := ditherAt(x, y)
			o := y*img.Stride + x*4
			for k := 0; k < 3; k++ {
				img.Pix[o+k] = uint8(math.Min(255, float64(img.Pix[o+k])*(1-a)+col[k]*a+u))
			}
		}
	}
}

// fillShapeGradient fills the layer's shape (a block, rounded or an ellipse) with a linear gradient from a to b. The shape's
// anti-aliased outline is drawn once as a coverage mask; the gradient is then computed pixel by pixel straight into dst, which is far
// cheaper than asking a generic painter for the colour of each pixel.
func fillShapeGradient(dst *image.RGBA, l *Layer, r rect, a, b color.NRGBA) {
	box := image.Rect(int(math.Floor(r.x)), int(math.Floor(r.y)), int(math.Ceil(r.x+r.w)), int(math.Ceil(r.y+r.h))).Intersect(dst.Bounds())
	if box.Empty() || r.w <= 0 || r.h <= 0 {
		return
	}
	mc := gg.NewContext(box.Dx(), box.Dy())
	shapePath(mc, l.Shape == "ellipse", rect{r.x - float64(box.Min.X), r.y - float64(box.Min.Y), r.w, r.h}, l.Radius)
	mc.SetColor(color.White)
	mc.Fill()
	cov := mc.Image().(*image.RGBA)

	rad := l.Angle * math.Pi / 180
	dx, dy := math.Sin(rad), -math.Cos(rad)
	half := (math.Abs(r.w*math.Sin(rad)) + math.Abs(r.h*math.Cos(rad))) / 2
	if half == 0 {
		half = 1
	}
	x0, y0 := r.x+r.w/2-dx*half, r.y+r.h/2-dy*half
	inv := 1 / (2 * half)
	pre := func(c color.NRGBA) [4]float64 {
		k := float64(c.A) / 255
		return [4]float64{float64(c.R) * k, float64(c.G) * k, float64(c.B) * k, float64(c.A)}
	}
	pa, pb := pre(a), pre(b)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			m := float64(cov.Pix[(y-box.Min.Y)*cov.Stride+(x-box.Min.X)*4+3]) / 255
			if m == 0 {
				continue
			}
			t := math.Min(1, math.Max(0, ((float64(x)+0.5-x0)*dx+(float64(y)+0.5-y0)*dy)*inv))
			srcA := (pa[3] + (pb[3]-pa[3])*t) / 255 * m // how much of the pixel this fill covers
			o := y*dst.Stride + x*4
			for k := 0; k < 3; k++ {
				src := (pa[k] + (pb[k]-pa[k])*t) * m
				dst.Pix[o+k] = uint8(src + float64(dst.Pix[o+k])*(1-srcA) + 0.5)
			}
			dst.Pix[o+3] = uint8(srcA*255 + float64(dst.Pix[o+3])*(1-srcA) + 0.5)
		}
	}
}
