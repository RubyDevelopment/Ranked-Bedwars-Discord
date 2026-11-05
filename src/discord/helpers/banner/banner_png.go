package banner

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/adler32"
	"hash/crc32"
	"image"
	"runtime"
	"sync"
)

// encodePNG writes img as a PNG. The standard encoder filters and compresses on one core, which is most of the time a banner takes
// to draw. Every banner is opaque, so this one writes it as 8-bit RGB and splits the work: the rows are filtered on all cores, then
// compressed in bands that each become a self-contained piece of one zlib stream (the way pigz does it). The picture is exactly the
// same (lossless); only the bytes of the compressed stream differ from what the standard encoder would write.
//
// An image with transparency goes to the standard encoder.
func encodePNG(img *image.RGBA) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 || !opaque(img) {
		var out bytes.Buffer
		err := pngEncoder.Encode(&out, img)
		return out.Bytes(), err
	}

	const bpp = 3
	stride := w * bpp
	rowLen := stride + 1 // each row starts with its filter type
	workers := min(runtime.GOMAXPROCS(0), 8, max(1, h/48))
	bands := splitRows(h, workers)

	// 1. Pack the pixels as RGB.
	rgb := getBuf(h * stride)
	defer putBuf(rgb)
	each(bands, func(from, to int) {
		for y := from; y < to; y++ {
			src := img.Pix[(y+b.Min.Y-img.Rect.Min.Y)*img.Stride+(b.Min.X-img.Rect.Min.X)*4:]
			dst := rgb[y*stride : (y+1)*stride]
			for x := 0; x < w; x++ {
				dst[x*3], dst[x*3+1], dst[x*3+2] = src[x*4], src[x*4+1], src[x*4+2]
			}
		}
	})

	// 2. Filter every row with whichever filter leaves the smallest numbers (they compress best), then compress each band.
	raw := getBuf(h * rowLen)
	defer putBuf(raw)
	parts := make([]bytes.Buffer, len(bands))
	var wg sync.WaitGroup
	for i, band := range bands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scratch := [3][]byte{make([]byte, stride), make([]byte, stride), make([]byte, stride)}
			zero := make([]byte, stride)
			for y := band[0]; y < band[1]; y++ {
				prev := zero
				if y > 0 {
					prev = rgb[(y-1)*stride : y*stride]
				}
				filterRow(raw[y*rowLen:(y+1)*rowLen], rgb[y*stride:(y+1)*stride], prev, &scratch)
			}
			fw := flateWriters.Get().(*flate.Writer)
			fw.Reset(&parts[i])
			_, _ = fw.Write(raw[band[0]*rowLen : band[1]*rowLen])
			if i == len(bands)-1 {
				_ = fw.Close() // the last band ends the stream
			} else {
				_ = fw.Flush() // the others end on a byte boundary, so the next band can follow them directly
			}
			flateWriters.Put(fw)
		}()
	}
	wg.Wait()

	// 3. Assemble: signature, header, the zlib stream (header, the bands, checksum) in IDAT chunks, end.
	size := 64
	for i := range parts {
		size += parts[i].Len()
	}
	out := bytes.NewBuffer(make([]byte, 0, size))
	out.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9] = 8, 2 // 8 bits per channel, truecolour
	writeChunk(out, "IHDR", ihdr)

	z := make([]byte, 0, size)
	z = append(z, 0x78, 0x9c)
	for i := range parts {
		z = append(z, parts[i].Bytes()...)
	}
	z = binary.BigEndian.AppendUint32(z, adler32.Checksum(raw))
	for len(z) > 0 {
		n := min(len(z), 1<<16)
		writeChunk(out, "IDAT", z[:n])
		z = z[n:]
	}
	writeChunk(out, "IEND", nil)
	return out.Bytes(), nil
}

var flateWriters = sync.Pool{New: func() any {
	fw, _ := flate.NewWriter(nil, flate.DefaultCompression)
	return fw
}}

func writeChunk(out *bytes.Buffer, kind string, data []byte) {
	var head [8]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(data)))
	copy(head[4:], kind)
	out.Write(head[:])
	out.Write(data)
	sum := crc32.NewIEEE()
	sum.Write(head[4:])
	sum.Write(data)
	out.Write(binary.BigEndian.AppendUint32(nil, sum.Sum32()))
}

// opaque reports whether every pixel is fully opaque.
func opaque(img *image.RGBA) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[(y-img.Rect.Min.Y)*img.Stride : (y-img.Rect.Min.Y)*img.Stride+b.Dx()*4]
		for x := 3; x < len(row); x += 4 {
			if row[x] != 0xff {
				return false
			}
		}
	}
	return true
}

// splitRows cuts rows 0..h into n consecutive [from, to) bands.
func splitRows(h, n int) [][2]int {
	bands := make([][2]int, 0, n)
	for i := 0; i < n; i++ {
		bands = append(bands, [2]int{h * i / n, h * (i + 1) / n})
	}
	return bands
}

// each runs f on every band at the same time and waits for all of them.
func each(bands [][2]int, f func(from, to int)) {
	var wg sync.WaitGroup
	for _, band := range bands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f(band[0], band[1])
		}()
	}
	wg.Wait()
}

// The PNG row filters: each stores the difference from a prediction (nothing, the pixel to the left, the one above, or the best of
// left, above and above-left), which is mostly small numbers.
const (
	filterNone  = 0
	filterSub   = 1
	filterUp    = 2
	filterPaeth = 4
)

// filterRow writes cur into out (out[0] is the filter type) using the filter that gives the smallest sum of magnitudes.
func filterRow(out, cur, prev []byte, scratch *[3][]byte) {
	n := len(cur)
	sub, up, paeth := scratch[0][:n], scratch[1][:n], scratch[2][:n]
	for i := 0; i < n; i++ {
		var left, upLeft byte
		if i >= 3 {
			left, upLeft = cur[i-3], prev[i-3]
		}
		sub[i] = cur[i] - left
		up[i] = cur[i] - prev[i]
		paeth[i] = cur[i] - paethPredict(left, prev[i], upLeft)
	}
	best, bestScore, data := filterNone, magnitude(cur), cur
	for _, c := range []struct {
		kind int
		rows []byte
	}{{filterSub, sub}, {filterUp, up}, {filterPaeth, paeth}} {
		if s := magnitude(c.rows); s < bestScore {
			best, bestScore, data = c.kind, s, c.rows
		}
	}
	out[0] = byte(best)
	copy(out[1:], data)
}

// magnitude sums the bytes as signed numbers' absolute values: the smaller, the better the row compresses.
func magnitude(row []byte) int {
	s := 0
	for _, v := range row {
		if v < 128 {
			s += int(v)
		} else {
			s += 256 - int(v)
		}
	}
	return s
}

func paethPredict(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// The pixel and row buffers of a banner are 3 MB each; they are reused from one render to the next instead of being allocated again.
var bufPool sync.Pool

func getBuf(n int) []byte {
	if p, ok := bufPool.Get().(*[]byte); ok && cap(*p) >= n {
		return (*p)[:n]
	}
	return make([]byte, n)
}

func putBuf(b []byte) { bufPool.Put(&b) }
