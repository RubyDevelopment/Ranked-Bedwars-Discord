package proofread

import (
	"image"
	"image/color"
	"testing"
)

// Light text on a dark scene comes out as dark text on a light ground, enlarged, with its brightness stretched to the full range.
func TestPrepareEnlargesAndInverts(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 100, 40))
	for y := range 40 {
		for x := range 100 {
			src.SetGray(x, y, color.Gray{Y: 40}) // dark scene
		}
	}
	for x := 10; x < 90; x++ { // one line of light "text"
		src.SetGray(x, 20, color.Gray{Y: 200})
		src.SetGray(x, 21, color.Gray{Y: 200})
	}
	out := prepare(src, 3)
	if b := out.Bounds(); b.Dx() != 300 || b.Dy() != 120 {
		t.Fatalf("size %v", b)
	}
	g := out.(*image.Gray)
	if scene, text := g.GrayAt(5, 5).Y, g.GrayAt(150, 61).Y; scene < 200 || text > 60 {
		t.Fatalf("scene %d (want light), text %d (want dark)", scene, text)
	}
}

func TestPrepareNeverExceedsThePixelBudget(t *testing.T) {
	out := prepare(image.NewGray(image.Rect(0, 0, 3000, 2000)), 4)
	if b := out.Bounds(); b.Dx()*b.Dy() > 9_100_000 {
		t.Fatalf("%d pixels", b.Dx()*b.Dy())
	}
}

func TestClamp(t *testing.T) {
	if clamp(0.5, 1, 3) != 1 || clamp(9, 1, 3) != 3 || clamp(2, 1, 3) != 2 {
		t.Fatal("clamp")
	}
}
