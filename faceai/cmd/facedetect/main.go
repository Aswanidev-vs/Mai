package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/mai/faceai"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <image_path> [cascade_path]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nFace detection tool using pigo (pure Go, no CGO required).\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s photo.jpg\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s photo.jpg ./data/faceai/facefinder\n", os.Args[0])
		os.Exit(1)
	}

	imagePath := os.Args[1]
	cascadePath := ""
	if len(os.Args) > 2 {
		cascadePath = os.Args[2]
	}

	img, err := loadImage(imagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading image: %v\n", err)
		os.Exit(1)
	}

	bounds := img.Bounds()
	fmt.Printf("Image: %s (%dx%d)\n", imagePath, bounds.Dx(), bounds.Dy())

	if cascadePath == "" {
		cascadePath = findCascadeFile()
	}

	detector, err := faceai.NewPigoDetector(cascadePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing detector: %v\n", err)
		fmt.Fprintf(os.Stderr, "Cascade file expected at: %s\n", cascadePath)
		os.Exit(1)
	}

	fmt.Printf("Cascade: %s\n", cascadePath)

	ctx := context.Background()
	faces, err := detector.Detect(ctx, img)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error detecting faces: %v\n", err)
		os.Exit(1)
	}

	if len(faces) == 0 {
		fmt.Println("\nNo faces detected.")
	} else {
		fmt.Printf("\nDetected %d face(s):\n", len(faces))
		for i, face := range faces {
			w := face.MaxX - face.MinX
			h := face.MaxY - face.MinY
			fmt.Printf("  Face %d: (%d, %d) - (%d, %d)  [%dx%d]\n",
				i+1, face.MinX, face.MinY, face.MaxX, face.MaxY, w, h)
		}
	}

	outputPath := strings.TrimSuffix(imagePath, filepath.Ext(imagePath)) + "_detected" + filepath.Ext(imagePath)
	if err := saveAnnotated(img, faces, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save annotated image: %v\n", err)
	} else {
		fmt.Printf("\nAnnotated image saved to: %s\n", outputPath)
	}
}

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return jpeg.Decode(f)
	case ".png":
		return png.Decode(f)
	default:
		f.Seek(0, 0)
		img, err := jpeg.Decode(f)
		if err != nil {
			f.Seek(0, 0)
			return png.Decode(f)
		}
		return img, nil
	}
}

func findCascadeFile() string {
	candidates := []string{
		"./data/faceai/facefinder",
		"../data/faceai/facefinder",
		"../../data/faceai/facefinder",
		"faceai/testserver/data/faceai/facefinder",
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "./data/faceai/facefinder"
}

func saveAnnotated(img image.Image, faces []faceai.Rect, outputPath string) error {
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)

	for _, face := range faces {
		drawRect(rgba, face, color.RGBA{R: 0, G: 255, B: 0, A: 255}, 3)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(outputPath))
	switch ext {
	case ".jpg", ".jpeg":
		return jpeg.Encode(f, rgba, &jpeg.Options{Quality: 95})
	case ".png":
		return png.Encode(f, rgba)
	default:
		return jpeg.Encode(f, rgba, &jpeg.Options{Quality: 95})
	}
}

func drawRect(img *image.RGBA, r faceai.Rect, c color.RGBA, thickness int) {
	for t := 0; t < thickness; t++ {
		x0 := r.MinX - t
		y0 := r.MinY - t
		x1 := r.MaxX + t
		y1 := r.MaxY + t

		bounds := img.Bounds()
		if x0 < bounds.Min.X {
			x0 = bounds.Min.X
		}
		if y0 < bounds.Min.Y {
			y0 = bounds.Min.Y
		}
		if x1 > bounds.Max.X {
			x1 = bounds.Max.X
		}
		if y1 > bounds.Max.Y {
			y1 = bounds.Max.Y
		}

		for x := x0; x <= x1; x++ {
			img.SetRGBA(x, y0, c)
			img.SetRGBA(x, y1, c)
		}
		for y := y0; y <= y1; y++ {
			img.SetRGBA(x0, y, c)
			img.SetRGBA(x1, y, c)
		}
	}
}
