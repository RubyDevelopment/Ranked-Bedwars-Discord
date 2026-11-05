package proofread

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"

	"github.com/danlock/gogosseract"
	"github.com/tetratelabs/wazero"
)

// The reader that needs nothing installed: Tesseract compiled to WebAssembly (gogosseract) with the English model (tessdata_fast) built in.
// It is used when no `tesseract` program is found; that one, when present, is faster.
//
//go:embed eng.traineddata
var engModel []byte

var wasm struct {
	mu    sync.Mutex // an instance is not safe for concurrent use; reads are rare, so they take turns
	cache wazero.CompilationCache
	byPSM map[string]*gogosseract.Tesseract
}

// recogniseWASM reads the picture with the built-in Tesseract. One instance is kept per page-layout mode, as the mode is set when it starts.
func recogniseWASM(ctx context.Context, img image.Image, psm string) ([]string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	wasm.mu.Lock()
	defer wasm.mu.Unlock()
	if wasm.byPSM == nil {
		wasm.byPSM = map[string]*gogosseract.Tesseract{}
		wasm.cache = wazero.NewCompilationCache()
	}
	t := wasm.byPSM[psm]
	if t == nil {
		var err error
		t, err = gogosseract.New(ctx, gogosseract.Config{
			Language:     "eng",
			TrainingData: bytes.NewReader(engModel),
			WASMCache:    wasm.cache,
			Variables:    map[string]string{"tessedit_pageseg_mode": psm, "preserve_interword_spaces": "1"},
		})
		if err != nil {
			return nil, fmt.Errorf("built-in tesseract: %w", err)
		}
		wasm.byPSM[psm] = t
	}
	if err := t.LoadImage(ctx, &buf, gogosseract.LoadImageOptions{}); err != nil {
		return nil, fmt.Errorf("built-in tesseract: %w", err)
	}
	text, err := t.GetText(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("built-in tesseract: %w", err)
	}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
