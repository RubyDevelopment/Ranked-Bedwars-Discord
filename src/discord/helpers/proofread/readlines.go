package proofread

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"sync"
)

// ReadLines reads the text lines of a screenshot. Chat sits in the lower left, so that part is cut out and enlarged until its letters are about
// the size the reader does best at; that size follows the game's own scale, found in the picture. The reads come in stages, each a few reads
// run side by side. In the first, a crisp screenshot is read as a mask of just the chat's text colors (quick, and usually enough) while the
// chat as a grey picture (slow, but works on any picture) is read beside it; if `enough` says the quick reads were enough, the slow one is
// dropped. Failing that, a wider piece and the whole picture are read.
func ReadLines(ctx context.Context, data []byte, enough func([]string) bool) ([]string, error) {
	bin, _ := tesseractPath() // "" when none is installed: the built-in reader is used
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the image could not be read: %w", err)
	}
	b := img.Bounds()
	// Chat is narrow (about 40% of the width at most) and low: a tighter piece has less of the scene to confuse the reader.
	chat := crop(img, image.Rect(b.Min.X, b.Min.Y+b.Dy()*35/100, b.Min.X+b.Dx()*52/100, b.Min.Y+b.Dy()*99/100))
	wide := crop(img, image.Rect(b.Min.X, b.Min.Y+b.Dy()*30/100, b.Max.X, b.Min.Y+b.Dy()*98/100))
	mask := textMask(chat, 30)
	fit := fitScale(mask, b.Dy())

	type pass struct {
		psm   string
		fast  bool // read first: if these are enough, the slower ones of the stage are not waited for
		build func() image.Image
	}
	grey := pass{"6", false, func() image.Image { return prepare(chat, fit) }}
	var stages [][]pass
	// The mask works on a screenshot with its pixels as the game drew them. A resized or compressed one (small, or the font's pixels blurred
	// together) has no exact colors left, so there it would only cost time: those go straight to the grey picture.
	if gs := guiScale(mask); gs >= 2 || gs == 1 && b.Dy() >= 900 {
		stages = append(stages, []pass{
			{"6", true, func() image.Image { return enlarge(mask, fit) }},
			{"4", true, func() image.Image { return enlarge(mask, fit*1.4) }}, // a second look: another layout mode, another size
			grey,
		})
	} else {
		stages = append(stages, []pass{grey, {"4", false, func() image.Image { return prepare(chat, fit*1.4) }}})
	}
	stages = append(stages, []pass{
		{"11", false, func() image.Image { return prepare(wide, fit) }},
		{"11", false, func() image.Image { return prepare(img, clamp(1800/float64(b.Dy()), 1, 3)) }},
	})

	var out []string
	seen := map[string]bool{}
	var firstErr error
	merge := func(lines []string) {
		for _, l := range lines {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	for _, stage := range stages {
		sctx, cancel := context.WithCancel(ctx)
		results := make([][]string, len(stage))
		errs := make([]error, len(stage))
		var fast, all sync.WaitGroup
		for i, p := range stage {
			all.Add(1)
			if p.fast {
				fast.Add(1)
			}
			go func() {
				defer all.Done()
				if p.fast {
					defer fast.Done()
				}
				results[i], errs[i] = recognise(sctx, bin, p.build(), p.psm)
			}()
		}
		take := func(onlyFast bool) { // merged in a fixed order, so the outcome does not depend on which read finished first
			for i, p := range stage {
				if onlyFast && !p.fast {
					continue
				}
				if errs[i] != nil && firstErr == nil && sctx.Err() == nil {
					firstErr = errs[i]
				}
				merge(results[i])
			}
		}
		fast.Wait()
		take(true)
		if enough != nil && enough(out) {
			cancel() // the slower reads of the stage are not needed
			return out, nil
		}
		all.Wait()
		take(false) // the rest of the stage, after the quick reads already merged
		cancel()
		if enough != nil && enough(out) {
			return out, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}
