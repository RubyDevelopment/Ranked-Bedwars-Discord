package skins

import (
	"encoding/base64"
	"io"
)

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func limited(r io.Reader, n int64) io.Reader { return io.LimitReader(r, n) }
