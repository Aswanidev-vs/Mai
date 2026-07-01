package faceai

import (
	"context"
	"image"
	"os"
	"testing"
)

func TestPipelineSetup(t *testing.T) {
	// Create temporary directory for data
	tmpDir, err := os.MkdirTemp("", "faceai_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		DataDir:        tmpDir,
		MatchThreshold: 0.5,
	}

	faceAI, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create faceai: %v", err)
	}

	// Create mock image
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))

	// Setup pipeline using mocks
	mockDetector := &mockDetector{}
	mockEmbedder := &PigoEmbedderMock{}
	err = faceAI.SetPipeline(mockDetector, mockEmbedder)
	if err != nil {
		t.Fatalf("failed to set pipeline: %v", err)
	}

	ctx := context.Background()
	err = faceAI.EnrollFromImages(ctx, CreatorID("creator1"), "Admin", []image.Image{img})
	if err != nil {
		t.Fatalf("enroll failed: %v", err)
	}

	faces, err := faceAI.RecognizeFrame(ctx, img)
	if err != nil {
		t.Fatalf("recognize failed: %v", err)
	}

	if len(faces) != 1 {
		t.Fatalf("expected 1 face, got %d", len(faces))
	}

	if faces[0].ID != CreatorID("creator1") || faces[0].Name != "Admin" {
		t.Errorf("unexpected recognized face: %+v", faces[0])
	}
}

func TestUnknownFaceReturnsPersonLabel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "faceai_unknown_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		DataDir:        tmpDir,
		MatchThreshold: 0.1, // very strict threshold to ensure no false match
	}

	faceAI, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create faceai: %v", err)
	}

	mockDetector := &mockDetector{}
	// Use embedder that returns high-dimensional vectors that won't match
	mockEmbedder := &noMatchEmbedder{}
	err = faceAI.SetPipeline(mockDetector, mockEmbedder)
	if err != nil {
		t.Fatalf("failed to set pipeline: %v", err)
	}

	ctx := context.Background()
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))

	// Enroll one person
	err = faceAI.EnrollFromImages(ctx, CreatorID("user1"), "TestUser", []image.Image{img})
	if err != nil {
		t.Fatalf("enroll failed: %v", err)
	}

	// Recognize the same image - but with no-match embedder, it should return Unknown
	faces, err := faceAI.RecognizeFrame(ctx, img)
	if err != nil {
		t.Fatalf("recognize failed: %v", err)
	}

	if len(faces) != 1 {
		t.Fatalf("expected 1 face, got %d", len(faces))
	}

	// Should return UnknownLabel "Person" for no match
	if faces[0].Name != UnknownLabel {
		t.Errorf("expected Name=%q for unknown face, got %q", UnknownLabel, faces[0].Name)
	}
	if faces[0].ID != "" {
		t.Errorf("expected empty ID for unknown face, got %q", faces[0].ID)
	}
}

type mockDetector struct{}

func (d *mockDetector) Detect(ctx context.Context, img image.Image) ([]Rect, error) {
	return []Rect{{MinX: 10, MinY: 10, MaxX: 90, MaxY: 90}}, nil
}

// noMatchEmbedder returns completely orthogonal vectors that will never match.
type noMatchEmbedder struct{}

func (e *noMatchEmbedder) Embed(ctx context.Context, face image.Image) ([]float64, error) {
	// Return a vector that's all zeros (max distance from any enrolled norm-1 vector)
	return make([]float64, 128), nil
}

func (e *noMatchEmbedder) Distance(a, b []float64) (float64, error) {
	return (&PigoEmbedderMock{}).Distance(a, b)
}
