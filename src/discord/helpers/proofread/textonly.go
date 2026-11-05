package proofread

import (
	"image"
	"image/color"

	xdraw "golang.org/x/image/draw"
)

// mcColors are the colors Minecraft draws chat text in (black and dark gray are left out: they are the shadows and the scene's own dark).
// The game's font has no smoothing, so a letter is exactly one of these colors.
var mcColors = [][3]int{
	{0, 0, 170}, {0, 170, 0}, {0, 170, 170}, {170, 0, 0}, {170, 0, 170}, {255, 170, 0}, {170, 170, 170},
	{85, 85, 255}, {85, 255, 85}, {85, 255, 255}, {255, 85, 85}, {255, 85, 255}, {255, 255, 85}, {255, 255, 255},
}

// textOnly keeps the pixels within a small distance of a chat text color as black on white, and enlarges that by `scale`. The scene behind the
// chat (sand, sky, a wall) is almost never exactly one of those colors, so it disappears, which the plain grey picture cannot do.
func textOnly(src image.Image, scale float64) image.Image {
	const tol = 30
	b := src.Bounds()
	mask := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			c := [3]int{int(r >> 8), int(g >> 8), int(bl >> 8)}
			v := uint8(255)
			for _, p := range mcColors {
				if abs(c[0]-p[0]) <= tol && abs(c[1]-p[1]) <= tol && abs(c[2]-p[2]) <= tol {
					v = 0
					break
				}
			}
			mask.SetGray(x, y, color.Gray{Y: v})
		}
	}
	big := image.NewGray(image.Rect(0, 0, max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))))
	xdraw.ApproxBiLinear.Scale(big, big.Bounds(), mask, mask.Bounds(), xdraw.Src, nil)
	return big
}
