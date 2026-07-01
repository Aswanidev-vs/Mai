package faceai

// Pipeline bundles the concrete implementations used by FaceAI.
type Pipeline struct {
	Detector Detector
	Embedder Embedder
}
