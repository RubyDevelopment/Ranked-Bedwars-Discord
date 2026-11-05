package proofread

import (
	"image"

	xdraw "golang.org/x/image/draw"
)

// mcColors are the colors Minecraft draws chat text in (black and dark gray are left out: they are the shadows and the scene's own dark).
// The game's font has no smoothing, so a letter is exactly one of these colors.
var mcColors = [][3]int{
	{0, 0, 170}, {0, 170, 0}, {0, 170, 170}, {170, 0, 0}, {170, 0, 170}, {255, 170, 0}, {170, 170, 170},
	{85, 85, 255}, {85, 255, 85}, {85, 255, 255}, {255, 85, 85}, {255, 85, 255}, {255, 255, 85}, {255, 255, 255},
}

// textMask keeps the pixels within tol of a chat text color as black on white. The scene behind the chat (sand, sky, a wall) is almost never
// exactly one of those colors, so it disappears, which a plain grey picture cannot do.
func textMask(src image.Image, tol int) *image.Gray {
	b := src.Bounds()
	mask := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	rgba, isRGBA := src.(*image.RGBA)
	nrgba, isNRGBA := src.(*image.NRGBA)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			var c [3]int
			switch { // the common formats are read straight from their bytes: asking the interface for each pixel is several times slower
			case isRGBA:
				o := rgba.PixOffset(b.Min.X+x, b.Min.Y+y)
				c = [3]int{int(rgba.Pix[o]), int(rgba.Pix[o+1]), int(rgba.Pix[o+2])}
			case isNRGBA:
				o := nrgba.PixOffset(b.Min.X+x, b.Min.Y+y)
				c = [3]int{int(nrgba.Pix[o]), int(nrgba.Pix[o+1]), int(nrgba.Pix[o+2])}
			default:
				r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
				c = [3]int{int(r >> 8), int(g >> 8), int(bl >> 8)}
			}
			v := uint8(255)
			for _, p := range mcColors {
				if abs(c[0]-p[0]) <= tol && abs(c[1]-p[1]) <= tol && abs(c[2]-p[2]) <= tol {
					v = 0
					break
				}
			}
			mask.Pix[y*mask.Stride+x] = v
		}
	}
	return mask
}

// enlarge scales a grey picture by `scale`.
func enlarge(m *image.Gray, scale float64) image.Image {
	b := m.Bounds()
	big := image.NewGray(image.Rect(0, 0, max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))))
	xdraw.ApproxBiLinear.Scale(big, big.Bounds(), m, b, xdraw.Src, nil)
	return big
}

// guiScale is how many screen pixels one pixel of the game's font is, read from the mask: the most common length of a run of text pixels
// along a line is a stroke of one font pixel. It is 2 or 3 on a 1080p screen, 4 or more on a 4K one. 0 when there is too little text to tell.
func guiScale(m *image.Gray) int {
	var hist [17]int
	b := m.Bounds()
	runs := 0
	for y := 0; y < b.Dy(); y += 2 { // every other line is plenty
		run := 0
		row := m.Pix[y*m.Stride : y*m.Stride+b.Dx()]
		for i := 0; i <= len(row); i++ {
			if i < len(row) && row[i] == 0 {
				run++
				continue
			}
			if run > 0 && run < len(hist) {
				hist[run]++
				runs++
			}
			run = 0
		}
	}
	if runs < 300 {
		return 0
	}
	best := 1
	for n := 2; n < len(hist); n++ {
		if hist[n] > hist[best] {
			best = n
		}
	}
	return best
}

// fitScale is how much to enlarge the chat so its letters are about the size the reader does best at (a capital about 28 pixels tall: the
// font's capitals are 7 font pixels). It follows the game's own scale, so a 4K screenshot is not blown up needlessly, which would only make the
// read slower, and a small one gets enough. Without a reading of the scale, the picture's height decides.
func fitScale(m *image.Gray, imageHeight int) float64 {
	gs := guiScale(m)
	if gs == 0 {
		gs = max(1, (imageHeight+270)/540)
	}
	return clamp(4/float64(gs), 1, 4)
}

// textOnly is the chat as a mask enlarged by `scale`.
func textOnly(src image.Image, scale float64) image.Image {
	return enlarge(textMask(src, 30), scale)
}
