package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/user/mai/faceai"
)

var (
	faceDetector faceai.Detector
	faceAI       faceai.FaceAI
)

func main() {
	dataDir := "./data/faceai"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("failed to create data dir: %v", err)
	}

	// Initialize face pipeline components dynamically based on build tags
	detector, embedder, err := initPipeline(dataDir)
	if err != nil {
		log.Fatalf("failed to init face pipeline: %v", err)
	}
	faceDetector = detector

	// Initialize faceai memory store
	cfg := faceai.Config{
		DataDir:        dataDir,
		MatchThreshold: 0.30,
	}
	memStore, err := faceai.New(cfg)
	if err != nil {
		log.Fatalf("failed to init faceai store: %v", err)
	}

	// Wire up the pipeline components
	err = memStore.SetPipeline(detector, embedder)
	if err != nil {
		log.Fatalf("failed to set pipeline: %v", err)
	}

	// Load existing identities
	if err := memStore.Load(); err != nil {
		log.Printf("Warning: failed to load database: %v", err)
	}

	faceAI = memStore

	// Setup HTTP Handlers
	http.Handle("/", http.FileServer(http.Dir("./static")))
	http.HandleFunc("/detect", handleDetect)
	http.HandleFunc("/enroll", handleEnroll)

	port := ":8082"
	log.Printf("Starting face test server on http://localhost%s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("server run failed: %v", err)
	}
}

type DetectResponse struct {
	Faces []faceai.RecognizedFace `json:"faces"`
}

func handleDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	img, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil {
		http.Error(w, "invalid jpeg image: "+err.Error(), http.StatusBadRequest)
		return
	}

	faces, err := faceAI.RecognizeFrame(r.Context(), img)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(DetectResponse{Faces: faces})
}

type EnrollRequest struct {
	Name string `json:"name"`
	// Single image (base64 JPEG data URL)
	ImageBase64 string `json:"image"`
	// Multiple images for better accuracy (array of base64 JPEG data URLs)
	Images []string `json:"images"`
}

func handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	var req EnrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}

	// Collect all images
	var images []image.Image

	// Single image field
	if req.ImageBase64 != "" {
		img, err := decodeBase64Image(req.ImageBase64)
		if err != nil {
			http.Error(w, "jpeg decode error: "+err.Error(), http.StatusBadRequest)
			return
		}
		images = append(images, img)
	}

	// Multiple images field
	for _, b64 := range req.Images {
		img, err := decodeBase64Image(b64)
		if err != nil {
			continue // skip bad images
		}
		images = append(images, img)
	}

	if len(images) == 0 {
		http.Error(w, "no valid images provided", http.StatusBadRequest)
		return
	}

	id := faceai.CreatorID(fmt.Sprintf("user_%d", time.Now().UnixNano()))
	ctx := r.Context()
	err := faceAI.EnrollFromImages(ctx, id, req.Name, images)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := faceAI.Save(); err != nil {
		log.Printf("Warning: failed to save db: %v", err)
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"success","images_enrolled":%d}`, len(images))
}

func decodeBase64Image(data string) (image.Image, error) {
	parts := strings.Split(data, ",")
	raw := data
	if len(parts) > 1 {
		raw = parts[1]
	}
	dec := base64.NewDecoder(base64.StdEncoding, strings.NewReader(raw))
	return jpeg.Decode(dec)
}
