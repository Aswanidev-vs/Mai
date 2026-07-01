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
		fmt.Fprintf(os.Stderr, "Usage: %s <command> [args...]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nCommands:\n")
		fmt.Fprintf(os.Stderr, "  detect <image>              Detect faces in an image\n")
		fmt.Fprintf(os.Stderr, "  enroll <image> <name>       Enroll a face from an image\n")
		fmt.Fprintf(os.Stderr, "  recognize <image>           Recognize faces in an image\n")
		fmt.Fprintf(os.Stderr, "  list                        List enrolled identities\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s detect photo.jpg\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s enroll person.jpg \"John Doe\"\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s recognize group.jpg\n", os.Args[0])
		os.Exit(1)
	}

	dataDir := "./faceai_data"
	os.MkdirAll(dataDir, 0755)

	cfg := faceai.Config{
		DataDir:        dataDir,
		MatchThreshold: 0.55,
	}

	store, err := faceai.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cascadePath := findCascadeFile()
	detector, err := faceai.NewPigoDetector(cascadePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing detector: %v\n", err)
		os.Exit(1)
	}

	embedder := &faceai.PigoEmbedderMock{}
	if err := store.SetPipeline(detector, embedder); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting pipeline: %v\n", err)
		os.Exit(1)
	}

	if err := store.Load(); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: could not load database: %v\n", err)
	}

	cmd := os.Args[1]
	ctx := context.Background()

	switch cmd {
	case "detect":
		cmdDetect(ctx, store, os.Args[2:])
	case "enroll":
		cmdEnroll(ctx, store, os.Args[2:])
	case "recognize":
		cmdRecognize(ctx, store, os.Args[2:])
	case "list":
		cmdList(store)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func cmdDetect(ctx context.Context, store faceai.FaceAI, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: facedetect detect <image>\n")
		os.Exit(1)
	}

	img, err := loadImage(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading image: %v\n", err)
		os.Exit(1)
	}

	faces, err := store.RecognizeFrame(ctx, img)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error detecting faces: %v\n", err)
		os.Exit(1)
	}

	if len(faces) == 0 {
		fmt.Println("No faces detected.")
	} else {
		fmt.Printf("Detected %d face(s):\n", len(faces))
		for i, face := range faces {
			w := face.BBox.MaxX - face.BBox.MinX
			h := face.BBox.MaxY - face.BBox.MinY
			status := "unknown"
			if face.Name != "" {
				status = face.Name
			}
			fmt.Printf("  Face %d: %s at (%d,%d)-(%d,%d) [%dx%d] dist=%.4f\n",
				i+1, status, face.BBox.MinX, face.BBox.MinY,
				face.BBox.MaxX, face.BBox.MaxY, w, h, face.Distance)
		}
	}

	outputPath := strings.TrimSuffix(args[0], filepath.Ext(args[0])) + "_detected.png"
	saveAnnotated(img, faces, outputPath)
	fmt.Printf("Saved: %s\n", outputPath)
}

func cmdEnroll(ctx context.Context, store faceai.FaceAI, args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: facedetect enroll <image> <name>\n")
		os.Exit(1)
	}

	img, err := loadImage(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading image: %v\n", err)
		os.Exit(1)
	}

	name := args[1]
	id := faceai.CreatorID("user_" + strings.ReplaceAll(strings.ToLower(name), " ", "_"))

	if err := store.EnrollFromImages(ctx, id, name, []image.Image{img}); err != nil {
		fmt.Fprintf(os.Stderr, "Error enrolling: %v\n", err)
		os.Exit(1)
	}

	if err := store.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save database: %v\n", err)
	}

	fmt.Printf("Enrolled \"%s\" (ID: %s)\n", name, id)
}

func cmdRecognize(ctx context.Context, store faceai.FaceAI, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: facedetect recognize <image>\n")
		os.Exit(1)
	}

	img, err := loadImage(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading image: %v\n", err)
		os.Exit(1)
	}

	faces, err := store.RecognizeFrame(ctx, img)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error recognizing faces: %v\n", err)
		os.Exit(1)
	}

	if len(faces) == 0 {
		fmt.Println("No faces detected.")
	} else {
		fmt.Printf("Detected %d face(s):\n", len(faces))
		for i, face := range faces {
			w := face.BBox.MaxX - face.BBox.MinX
			h := face.BBox.MaxY - face.BBox.MinY
			status := "UNKNOWN"
			if face.Name != "" {
				status = face.Name
			}
			fmt.Printf("  Face %d: %s  bbox=(%d,%d)-(%d,%d) [%dx%d] dist=%.4f\n",
				i+1, status, face.BBox.MinX, face.BBox.MinY,
				face.BBox.MaxX, face.BBox.MaxY, w, h, face.Distance)
		}
	}

	outputPath := strings.TrimSuffix(args[0], filepath.Ext(args[0])) + "_recognized.png"
	saveAnnotated(img, faces, outputPath)
	fmt.Printf("Saved: %s\n", outputPath)
}

func cmdList(store faceai.FaceAI) {
	fmt.Println("Enrolled identities:")
	fmt.Println("  (Use the testserver web UI to manage enrollments)")
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
		"../../faceai/testserver/data/faceai/facefinder",
		"faceai/testserver/data/faceai/facefinder",
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "./data/faceai/facefinder"
}

func saveAnnotated(img image.Image, faces []faceai.RecognizedFace, outputPath string) {
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)

	for _, face := range faces {
		c := color.RGBA{R: 255, G: 0, B: 0, A: 255}
		if face.Name != "" {
			c = color.RGBA{R: 0, G: 255, B: 0, A: 255}
		}
		drawRect(rgba, face.BBox, c, 3)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return
	}
	defer f.Close()

	png.Encode(f, rgba)
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
