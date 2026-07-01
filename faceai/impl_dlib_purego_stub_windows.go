//go:build windows && purego
// +build windows,purego

package faceai

import (
	"context"
	"errors"
	"image"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

type dlibEmbedderPurego struct {
	dllHandle uintptr
	embedFunc func(imgPtr unsafe.Pointer, width, height, stride int, outDescriptor *float32) int
}

// NewDlibEmbedderPurego returns an Embedder that loads a C-wrapped dlib DLL at runtime via purego.
func NewDlibEmbedderPurego(dllPath string) (Embedder, error) {
	if dllPath == "" {
		return nil, errors.New("dllPath is empty")
	}

	lib, err := purego.Open(dllPath)
	if err != nil {
		return nil, err
	}

	var embedFunc func(imgPtr unsafe.Pointer, width, height, stride int, outDescriptor *float32) int
	purego.RegisterLibFunc(&embedFunc, lib, "dlib_embed_face")

	return &dlibEmbedderPurego{
		dllHandle: lib,
		embedFunc: embedFunc,
	}, nil
}

func (e *dlibEmbedderPurego) Embed(ctx context.Context, face image.Image) ([]float64, error) {
	if e.embedFunc == nil {
		return nil, errors.New("embed function not loaded")
	}

	rgba := imgToRGBA(face)
	w := rgba.Bounds().Dx()
	h := rgba.Bounds().Dy()
	stride := rgba.Stride

	var descriptor [128]float32
	ret := e.embedFunc(unsafe.Pointer(&rgba.Pix[0]), w, h, stride, &descriptor[0])
	if ret != 0 {
		return nil, errors.New("failed to extract face embedding from DLL")
	}

	out := make([]float64, 128)
	for i, val := range descriptor {
		out[i] = float64(val)
	}
	return out, nil
}

func (e *dlibEmbedderPurego) Distance(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, errors.New("embedding size mismatch")
	}
	var sum float64
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum, nil
}

// Close closes the loaded dynamic library handle.
func (e *dlibEmbedderPurego) Close() error {
	return syscall.FreeLibrary(syscall.Handle(e.dllHandle))
}
