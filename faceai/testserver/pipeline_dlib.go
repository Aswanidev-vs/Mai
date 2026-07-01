//go:build dlib
// +build dlib

package main

import (
	"log"
	"path/filepath"

	"github.com/user/mai/faceai"
)

func initPipeline(dataDir string) (faceai.Detector, faceai.Embedder, error) {
	modelsDir := filepath.Join(dataDir, "dlib_models")
	log.Printf("Attempting to initialize pipeline with Dlib (models: %s)...", modelsDir)
	
	pipeline, err := faceai.NewGoFacePipeline(modelsDir)
	if err == nil {
		log.Println("Dlib initialized successfully.")
		return pipeline, pipeline, nil
	}

	log.Printf("Dlib initialization failed: %v. Falling back to Pigo...", err)

	// Fallback to Pigo detector + mock embedder
	cascadePath := filepath.Join(dataDir, "facefinder")
	detector, err := faceai.NewPigoDetector(cascadePath)
	if err != nil {
		return nil, nil, err
	}
	return detector, &faceai.PigoEmbedderMock{}, nil
}
