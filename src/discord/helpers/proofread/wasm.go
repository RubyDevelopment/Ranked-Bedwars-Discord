package proofread

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/png"
	"runtime"
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

// maxReaders is how many reads run at once. Each holds the model in memory (tens of MB) and keeps a core busy.
// Half the cores (at least one, at most three) so the bot's own work always has a core left while screenshots are read.
var maxReaders = max(1, min(3, runtime.NumCPU()/2))

var wasm struct {
	mu    sync.Mutex
	cache wazero.CompilationCache // the compiled WebAssembly, shared by every instance: only the first one pays for compiling
	free  map[string][]*gogosseract.Tesseract
	slots chan struct{}
}

func wasmInit() {
	if wasm.free == nil {
		wasm.free = map[string][]*gogosseract.Tesseract{}
		wasm.cache = wazero.NewCompilationCache()
		wasm.slots = make(chan struct{}, maxReaders)
	}
}

// take gives a reader set to page-layout mode psm: an idle one, or a new one. An instance is not safe for concurrent use, so each read has
// its own; give it back with put.
func takeReader(ctx context.Context, psm string) (*gogosseract.Tesseract, error) {
	wasm.mu.Lock()
	wasmInit()
	slots := wasm.slots
	wasm.mu.Unlock()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	wasm.mu.Lock()
	if list := wasm.free[psm]; len(list) > 0 {
		t := list[len(list)-1]
		wasm.free[psm] = list[:len(list)-1]
		wasm.mu.Unlock()
		return t, nil
	}
	cache := wasm.cache
	wasm.mu.Unlock()
	// Made with a context of its own: it outlives the read that happened to need it.
	t, err := gogosseract.New(context.Background(), gogosseract.Config{
		Language:     "eng",
		TrainingData: bytes.NewReader(engModel),
		WASMCache:    cache,
		Variables:    map[string]string{"tessedit_pageseg_mode": psm, "preserve_interword_spaces": "1"},
	})
	if err != nil {
		<-slots
		return nil, fmt.Errorf("built-in tesseract: %w", err)
	}
	return t, nil
}

// putReader gives a reader back; one that failed is dropped instead, in case it is left in a bad state.
func putReader(psm string, t *gogosseract.Tesseract, ok bool) {
	wasm.mu.Lock()
	if ok {
		wasm.free[psm] = append(wasm.free[psm], t)
	}
	slots := wasm.slots
	wasm.mu.Unlock()
	if !ok {
		_ = t.Close(context.Background())
	}
	<-slots
}

// Warm starts the built-in readers ahead of time, so the first screenshot does not wait for the model to load and the WebAssembly to compile.
func Warm() {
	if _, err := tesseractPath(); err == nil {
		return // a tesseract program is installed: nothing to prepare
	}
	for _, psm := range []string{"6", "4"} {
		if t, err := takeReader(context.Background(), psm); err == nil {
			putReader(psm, t, true)
		}
	}
}

// recogniseWASM reads the picture with the built-in Tesseract. Reads of different pictures run side by side, up to maxReaders.
func recogniseWASM(ctx context.Context, img image.Image, psm string) ([]string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	t, err := takeReader(ctx, psm)
	if err != nil {
		return nil, err
	}
	// The WebAssembly cannot be interrupted: gogosseract builds its runtime without wazero's close-on-context-done, so a read ignores its
	// deadline and runs to the end. Waiting for it here froze the submission (and every read queued behind it) for as long as it took.
	// So the read runs on its own and is walked away from when the context ends; it keeps its slot until it finishes, which is what
	// stops a pile of slow reads from being started on top of it. ponytail: an abandoned read still burns its core until it ends.
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := func() (string, error) {
			if err := t.LoadImage(ctx, &buf, gogosseract.LoadImageOptions{}); err != nil {
				return "", err
			}
			return t.GetText(ctx, nil)
		}()
		putReader(psm, t, err == nil)
		done <- result{text, err}
	}()
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return nil, fmt.Errorf("built-in tesseract: %w", ctx.Err())
	}
	if res.err != nil {
		return nil, fmt.Errorf("built-in tesseract: %w", res.err)
	}
	var lines []string
	for _, l := range strings.Split(res.text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
