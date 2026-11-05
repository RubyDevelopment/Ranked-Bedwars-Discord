package proofread

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestBuiltInReader (with no tesseract program installed) reads a real screenshot given in OCR_TEST_IMAGE with the built-in Tesseract (skipped without it).
func TestBuiltInReader(t *testing.T) {
	path := os.Getenv("OCR_TEST_IMAGE")
	if path == "" {
		t.Skip("set OCR_TEST_IMAGE to a screenshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	lines, err := ReadLines(context.Background(), data, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d lines in %s", len(lines), time.Since(start))
	for _, l := range lines {
		t.Log(l)
	}
	if len(lines) == 0 {
		t.Fatal("nothing read")
	}
}
