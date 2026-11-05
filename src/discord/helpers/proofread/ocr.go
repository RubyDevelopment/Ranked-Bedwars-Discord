package proofread

import (
	"bytes"
	"context"
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
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// ErrNoOCR means the text reader (Tesseract) is not installed on this machine, so screenshots cannot be read.
var ErrNoOCR = errors.New("tesseract is not installed")

const maxImageBytes = 12 << 20

func tesseractPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("TESSERACT_PATH")); p != "" {
		return p, nil
	}
	p, err := exec.LookPath("tesseract")
	if err != nil {
		return "", ErrNoOCR
	}
	return p, nil
}

// Available reports whether screenshots can be read here.
func Available() bool { _, err := tesseractPath(); return err == nil }

// Download fetches an image the bot was handed as a Discord attachment. Only Discord's own CDN is allowed: the address is a user's input.
func Download(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !(u.Host == "cdn.discordapp.com" || u.Host == "media.discordapp.net") {
		return nil, fmt.Errorf("not a Discord attachment address")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("the image is larger than %d MB", maxImageBytes>>20)
	}
	return data, nil
}

// ReadLines reads the text lines of a screenshot, zooming as it needs to. Chat sits in the lower left, so that part is cut out and enlarged
// until its letters are about the size the reader does best at; the size comes from the picture's height, since a screenshot's game
// scale only changes how big the letters are, not where they are. If `enough` says what was read is not enough, it tries again enlarged
// further with another page-layout mode, and last reads the whole picture. A clear screenshot therefore costs one quick read.
func ReadLines(ctx context.Context, data []byte, enough func([]string) bool) ([]string, error) {
	bin, err := tesseractPath()
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the image could not be read: %w", err)
	}
	b := img.Bounds()
	chat := crop(img, image.Rect(b.Min.X, b.Min.Y+b.Dy()*30/100, b.Min.X+b.Dx()*65/100, b.Min.Y+b.Dy()*98/100))
	fit := clamp(2200/float64(b.Dy()), 1.5, 4.5)
	passes := []struct {
		src   image.Image
		scale float64
		psm   string
	}{
		{chat, fit, "6"},
		{chat, clamp(fit*1.6, 2, 6), "11"},
		{img, clamp(1800/float64(b.Dy()), 1, 3), "11"},
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range passes {
		lines, err := recognise(ctx, bin, prepare(p.src, p.scale), p.psm)
		if err != nil {
			return nil, err
		}
		for _, l := range lines {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
		if enough != nil && enough(out) {
			break
		}
	}
	return out, nil
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func crop(src image.Image, r image.Rectangle) image.Image {
	if s, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	return dst
}

// prepare makes the picture easier for the reader: enlarged by `scale`, grey, stretched to the full range of brightness, and dark text on a
// light ground (chat is light text on a dark scene). A picture that would come out larger than maxPixels is enlarged less.
func prepare(src image.Image, scale float64) image.Image {
	const maxPixels = 9_000_000
	b := src.Bounds()
	if px := float64(b.Dx()) * float64(b.Dy()) * scale * scale; px > maxPixels {
		scale *= math.Sqrt(maxPixels / px)
	}
	w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	big := image.NewRGBA(image.Rect(0, 0, w, h))
	if scale >= 1 {
		xdraw.CatmullRom.Scale(big, big.Bounds(), src, b, xdraw.Src, nil)
	} else {
		xdraw.ApproxBiLinear.Scale(big, big.Bounds(), src, b, xdraw.Src, nil)
	}
	out := image.NewGray(big.Bounds())
	var hist [256]int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g := color.GrayModel.Convert(big.At(x, y)).(color.Gray)
			out.SetGray(x, y, g)
			hist[g.Y]++
		}
	}
	// stretch the 1st..99th percentile of brightness to 0..255 and invert
	total, lo, hi, acc := w*h, 0, 255, 0
	for i := 0; i < 256; i++ {
		if acc += hist[i]; acc >= total/100 {
			lo = i
			break
		}
	}
	acc = 0
	for i := 255; i >= 0; i-- {
		if acc += hist[i]; acc >= total/100 {
			hi = i
			break
		}
	}
	span := float64(max(1, hi-lo))
	for i, v := range out.Pix {
		n := (float64(v) - float64(lo)) / span
		out.Pix[i] = uint8(255 - math.Max(0, math.Min(255, n*255)))
	}
	return out
}

func recognise(ctx context.Context, bin string, img image.Image, psm string) ([]string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "stdin", "stdout", "--psm", psm, "-l", "eng", "-c", "preserve_interword_spaces=1")
	cmd.Stdin = &buf
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tesseract: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var lines []string
	for _, l := range strings.Split(stdout.String(), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
