//go:build !dlib
// +build !dlib

package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/user/mai/faceai"
)

func initPipeline(dataDir string) (faceai.Detector, faceai.Embedder, error) {
	cascadePath := filepath.Join(dataDir, "facefinder")
	if _, err := os.Stat(cascadePath); os.IsNotExist(err) {
		log.Fatalf("Error: Local cascade file 'facefinder' not found at %s.\n"+
			"Please copy the Pigo cascade file 'facefinder' from your local system or download it "+
			"manually to this path before starting.", cascadePath)
	}

	log.Printf("Initializing pipeline with Pigo (cascade: %s)", cascadePath)
	detector, err := faceai.NewPigoDetector(cascadePath)
	if err != nil {
		return nil, nil, err
	}
	return detector, &faceai.PigoEmbedderMock{}, nil
}
