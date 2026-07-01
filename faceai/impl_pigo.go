package faceai

import (
	"context"
	"errors"
	"image"
	"math"
	"os"

	pigo "github.com/esimov/pigo/core"
)

type PigoDetector struct {
	classifier *pigo.Pigo
}

// NewPigoDetector loads the pigo cascade classifier file (typically "facefinder").
func NewPigoDetector(cascadePath string) (Detector, error) {
	b, err := os.ReadFile(cascadePath)
	if err != nil {
		return nil, err
	}
	p := pigo.NewPigo()
	classifier, err := p.Unpack(b)
	if err != nil {
		return nil, err
	}
	return &PigoDetector{classifier: classifier}, nil
}

func (d *PigoDetector) Detect(ctx context.Context, img image.Image) ([]Rect, error) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	pixels := pigo.RgbToGrayscale(img)

	cParams := pigo.CascadeParams{
		MinSize:     20,
		MaxSize:     1000,
		ShiftFactor: 0.1,
		ScaleFactor: 1.1,
		ImageParams: pigo.ImageParams{
			Pixels: pixels,
			Rows:   h,
			Cols:   w,
			Dim:    w,
		},
	}

	// Make face detection stricter to reduce false positives/mis-crops.
	// False positives are a major cause of "other person -> my name".
	results := d.classifier.RunCascade(cParams, 0.5)
	// Tighten clustering so we don't merge nearby faces into one box.
	results = d.classifier.ClusterDetections(results, 0.10)

	var rects []Rect
	for _, r := range results {
		// Increase quality threshold.
		if r.Q > 4.5 {
			// Pigo gives center (Col, Row) and a scale for the face size.
			// Use an aspect-consistent rect (approx 120x150 => h/w = 1.25)
			// rather than a square box to reduce partial-face embeddings.
			w := int(r.Scale)
			if w < 20 {
				w = 20
			}
			h := int(float64(w) * 1.25)

			minX := int(r.Col) - w/2
			minY := int(r.Row) - h/2
			maxX := int(r.Col) + w/2
			maxY := int(r.Row) + h/2

			minX = clampInt(minX, bounds.Min.X, bounds.Max.X)
			minY = clampInt(minY, bounds.Min.Y, bounds.Max.Y)
			maxX = clampInt(maxX, bounds.Min.X, bounds.Max.X)
			maxY = clampInt(maxY, bounds.Min.Y, bounds.Max.Y)

			if maxX > minX && maxY > minY {
				rects = append(rects, Rect{
					MinX: minX,
					MinY: minY,
					MaxX: maxX,
					MaxY: maxY,
				})
			}
		}
	}

	return rects, nil
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ---- Face Recognition using Multi-Block LBP ----

const (
	embedSize   = 128
	faceNormW   = 120 // normalized face width
	faceNormH   = 150 // normalized face height (maintains aspect ratio)
	lbpGridRows = 6   // rows in LBP grid
	lbpGridCols = 5   // columns in LBP grid
	lbpBins     = 8   // bins per LBP cell (reduced from 256 for compactness)
)

// PigoEmbedderMock performs face recognition using multi-block Local Binary Patterns.
// This is the classic "original" face recognition approach that works without neural networks.
// It divides the face into a grid, computes LBP histograms per cell, and uses
// chi-square distance for comparison.
type PigoEmbedderMock struct{}

func (e *PigoEmbedderMock) Embed(ctx context.Context, face image.Image) ([]float64, error) {
	// Normalize face to fixed size for consistent feature extraction
	norm := rasterize(face, faceNormW, faceNormH)
	if norm == nil {
		return make([]float64, embedSize), nil
	}
	gray := toGray(norm)

	// Compute multi-block LBP features
	// We divide face into lbpGridRows x lbpGridCols cells
	// Each cell produces lbpBins histogram values
	// Total: lbpGridRows * lbpGridCols * lbpBins = 6 * 5 * 8 = 240 dims
	// We'll compress to 128 by using only the best cells

	h := len(gray)
	w := len(gray[0])

	// Build feature vector using spatial pyramid of LBP histograms.
	// This multi-resolution approach captures face texture at different scales.
	vec := make([]float64, 0, embedSize)

	// Level 0: Full-face global histogram (8 bins)
	globalLBP := lbpHistogramUniform(gray, 0, 0, w, h, 8)
	vec = append(vec, globalLBP...)

	// Level 1: 2x2 grid (4 cells * 8 bins = 32)
	cellH2 := h / 2
	cellW2 := w / 2
	for row := 0; row < 2; row++ {
		for col := 0; col < 2; col++ {
			x0 := col * cellW2
			y0 := row * cellH2
			hist := lbpHistogramUniform(gray, x0, y0, cellW2, cellH2, 8)
			vec = append(vec, hist...)
		}
	}

	// Level 2: 3x3 grid (9 cells * 8 bins = 72) - but take only center 5 cells (40 bins)
	// This focuses on face interior (eyes, nose, mouth) and reduces background
	cellH3 := h / 3
	cellW3 := w / 3
	for row := 0; row < 3; row++ {
		for col := 0; col < 3; col++ {
			// Skip corner cells (background regions)
			if (row == 0 || row == 2) && (col == 0 || col == 2) {
				continue
			}
			x0 := col * cellW3
			y0 := row * cellH3
			hist := lbpHistogramUniform(gray, x0, y0, cellW3, cellH3, 8)
			vec = append(vec, hist...)
		}
	}

	// Total so far: 8 + 32 + 40 = 80. Need 48 more.
	// Level 3: Eye region (top-center cell from 3x3) and nose region (center)
	// Split eye region into 3 sub-regions
	eyeCol := 1 * cellW3
	eyeRow := 0
	eyeW := cellW3
	eyeH := cellH3
	for sub := 0; sub < 3; sub++ {
		subX := eyeCol + (sub%2)*eyeW/3
		subY := eyeRow + (sub/2)*eyeH/2
		subW := eyeW / 3
		subH := eyeH / 2
		hist := lbpHistogramUniform(gray, subX, subY, subW, subH, 8)
		vec = append(vec, hist...)
	}

	// Nose region: split into 2
	noseCol := 1 * cellW3
	noseRow := 1 * cellH3
	noseW := cellW3
	noseH := cellH3
	for sub := 0; sub < 2; sub++ {
		subX := noseCol
		subY := noseRow + sub*noseH/2
		hist := lbpHistogramUniform(gray, subX, subY, noseW, noseH/2, 8)
		vec = append(vec, hist...)
	}

	// Mouth region: 1 more
	mouthCol := 1 * cellW3
	mouthRow := 2 * cellH3
	hist := lbpHistogramUniform(gray, mouthCol, mouthRow, cellW3, cellH3, 8)
	vec = append(vec, hist...)

	// Total: 8 + 32 + 40 + 24 + 16 + 8 = 128 ✓

	// Use L2 normalization on the concatenated features
	// (This converts the multi-block LBP into a form suitable for cosine distance)
	l2Normalize(vec)

	return vec, nil
}

func (e *PigoEmbedderMock) Distance(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, errors.New("size mismatch")
	}
	// Cosine distance: 1 - cosine_similarity
	// Range [0,2] where 0 = identical, 1 = orthogonal, 2 = opposite
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom < 1e-12 {
		return 1.0, nil
	}
	sim := dot / denom
	if sim > 1 {
		sim = 1
	}
	if sim < -1 {
		sim = -1
	}
	return 1.0 - sim, nil
}

// ---- LBP Feature Extraction ----

// lbpHistogramUniform computes an LBP histogram for a region.
// Uses a reduced-bin approach: maps the 256 possible LBP codes into `bins` bins.
func lbpHistogramUniform(gray [][]float64, x0, y0, w, h, bins int) []float64 {
	rows := len(gray)
	cols := len(gray[0])
	hist := make([]float64, bins)
	count := 0

	for y := y0 + 1; y < y0+h-1 && y < rows-1; y++ {
		for x := x0 + 1; x < x0+w-1 && x < cols-1; x++ {
			code := lbpValue(gray, x, y, rows, cols)
			// Map the 8-bit LBP code to a bin
			// Use uniform pattern concept: codes with ≤ 2 transitions are "uniform"
			// and get their own bin; all non-uniform codes go to the last bin
			uniform := isUniformLBP(code)
			var bin int
			if uniform {
				// For uniform patterns, spread across the first (bins-1) bins
				// Count the number of 1-bits as a simple descriptor
				bits := popcount(uint8(code))
				bin = bits
				if bin >= bins-1 {
					bin = bins - 2
				}
			} else {
				// Non-uniform patterns go to the last bin
				bin = bins - 1
			}
			if bin < bins {
				hist[bin] += 1.0
				count++
			}
		}
	}

	if count > 0 {
		// L1 normalize then sqrt (Hellinger distance preparation)
		invCount := 1.0 / float64(count)
		for i := range hist {
			hist[i] = math.Sqrt(hist[i] * invCount)
		}
	}

	return hist
}

// isUniformLBP returns true if the LBP code has at most 2 bitwise transitions.
func isUniformLBP(code int) bool {
	// Count transitions in the circular 8-bit pattern
	transitions := 0
	for i := 0; i < 8; i++ {
		bit1 := (code >> uint(i)) & 1
		bit2 := (code >> uint((i+1)%8)) & 1
		if bit1 != bit2 {
			transitions++
		}
	}
	return transitions <= 2
}

// popcount counts the number of 1 bits in a byte.
func popcount(x uint8) int {
	// Simple bit count
	count := 0
	for x != 0 {
		count++
		x &= x - 1
	}
	return count
}

// ---- pixel type and helpers (unchanged) ----

// pixel holds normalized [0..1] RGB values.
type pixel struct {
	r, g, b float64
}

// toGray converts the pixel grid to a grayscale grid.
func toGray(grid [][]pixel) [][]float64 {
	h := len(grid)
	w := len(grid[0])
	gray := make([][]float64, h)
	for y := 0; y < h; y++ {
		gray[y] = make([]float64, w)
		for x := 0; x < w; x++ {
			p := grid[y][x]
			gray[y][x] = 0.299*p.r + 0.587*p.g + 0.114*p.b
		}
	}
	return gray
}

// rasterize resizes the image to w×h and returns a grid of normalized pixels.
func rasterize(img image.Image, w, h int) [][]pixel {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW == 0 || srcH == 0 {
		return nil
	}

	grid := make([][]pixel, h)
	for y := 0; y < h; y++ {
		grid[y] = make([]pixel, w)
		for x := 0; x < w; x++ {
			srcX := float64(x)*float64(srcW)/float64(w) + 0.5
			srcY := float64(y)*float64(srcH)/float64(h) + 0.5
			ix := int(srcX)
			iy := int(srcY)
			if ix >= srcW {
				ix = srcW - 1
			}
			if iy >= srcH {
				iy = srcH - 1
			}
			r, g, b, _ := img.At(bounds.Min.X+ix, bounds.Min.Y+iy).RGBA()
			grid[y][x] = pixel{
				r: float64(r) / 65535.0,
				g: float64(g) / 65535.0,
				b: float64(b) / 65535.0,
			}
		}
	}
	return grid
}

func lbpValue(gray [][]float64, x, y, rows, cols int) int {
	c := gray[y][x]
	bits := 0
	neighbors := [8][2]int{
		{-1, -1}, {0, -1}, {1, -1}, {1, 0},
		{1, 1}, {0, 1}, {-1, 1}, {-1, 0},
	}
	for i, n := range neighbors {
		nx, ny := x+n[0], y+n[1]
		if nx < 0 || nx >= cols || ny < 0 || ny >= rows {
			continue
		}
		if gray[ny][nx] >= c {
			bits |= 1 << uint(i)
		}
	}
	return bits
}

func copyN(dst, src []float64, n int) {
	for i := 0; i < n && i < len(src) && i < len(dst); i++ {
		dst[i] = src[i]
	}
}

func l2Normalize(v []float64) {
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm < 1e-12 {
		return
	}
	for i := range v {
		v[i] /= norm
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
