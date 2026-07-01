package faceai

import (
	"context"
	"image"
	"time"
)

// UnknownLabel is the display name shown for unrecognized faces.
const UnknownLabel = "Person"

// CreatorID is an ID assigned to a known creator.
type CreatorID string

// RecognizedFace represents a single detected face and its best match (or Unknown).
type RecognizedFace struct {
	// ID is empty when unknown.
	ID CreatorID
	// Name is empty when unknown.
	Name string

	// Distance is smaller when the match is better.
	Distance float64

	// Bounding box in the coordinate space of the input frame.
	// Implementations may return a zero rect when not available.
	BBox Rect

	// Timestamp for debugging/telemetry.
	At time.Time
}

// Rect is a lightweight bounding box.
type Rect struct {
	MinX int
	MinY int
	MaxX int
	MaxY int
}

type Detector interface {
	// Detect returns one face crop/region per face.
	Detect(ctx context.Context, frame image.Image) ([]Rect, error)
}

// Embedder compares a face crop to known embeddings and returns the best match.
type Embedder interface {
	// Embed returns an embedding vector for the given face crop.
	Embed(ctx context.Context, face image.Image) ([]float64, error)

	// Distance computes a distance between two embedding vectors.
	Distance(a, b []float64) (float64, error)
}

type Recognizer interface {
	// RecognizeFrame returns best matches for all faces in the frame.
	RecognizeFrame(ctx context.Context, frame image.Image) ([]RecognizedFace, error)
}

// Enroller enrolls known creators by computing embeddings from images.
type Enroller interface {
	EnrollFromImages(ctx context.Context, id CreatorID, name string, imgs []image.Image) error
}

// FaceAI is the main library API.
type FaceAI interface {
	Enroller
	Recognizer

	// Save persists the in-memory dataset to disk.
	Save() error
	// Load loads dataset from disk, replacing the current in-memory dataset.
	Load() error
}

// Config configures where to store data and recognition thresholds.
type Config struct {
	// DataDir is where persistent files are stored (default: "./data").
	DataDir string

	// MatchThreshold is the max distance allowed for a match to be considered known.
	// Smaller is stricter. (Implementation-dependent; typical range ~0.25..0.8)
	MatchThreshold float64

	// EmbeddingPerEnrollment optionally limits how many embeddings to store per enrollment.
	// Some implementations may store one embedding per image; others may store an aggregate.
	EmbeddingPerEnrollment int
}
