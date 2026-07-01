package faceai

import "image"

func cropOrFull(img image.Image, r Rect) image.Image {
	// If Rect looks invalid, just use full frame.
	if r.MaxX <= r.MinX || r.MaxY <= r.MinY {
		return img
	}
	b := img.Bounds()

	// Add padding around the detected face box to stabilize embeddings and reduce
	// identity mix due to tight/partial crops.
	// Padding is based on the box size (percent), not absolute pixels.
	boxW := r.MaxX - r.MinX
	boxH := r.MaxY - r.MinY
	if boxW <= 0 || boxH <= 0 {
		return img
	}

	const padFrac = 0.18 // 18% padding on each side
	padX := int(float64(boxW) * padFrac)
	padY := int(float64(boxH) * padFrac)

	minX := r.MinX - padX
	minY := r.MinY - padY
	maxX := r.MaxX + padX
	maxY := r.MaxY + padY

	minX = clamp(minX, b.Min.X, b.Max.X)
	minY = clamp(minY, b.Min.Y, b.Max.Y)
	maxX = clamp(maxX, b.Min.X, b.Max.X)
	maxY = clamp(maxY, b.Min.Y, b.Max.Y)

	if maxX <= minX || maxY <= minY {
		return img
	}

	// Prefer SubImage when supported.
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if s, ok := img.(subImager); ok {
		return s.SubImage(image.Rect(minX, minY, maxX, maxY))
	}

	// If not supported, fall back to full frame.
	return img
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
