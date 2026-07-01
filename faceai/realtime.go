package faceai

import (
	"context"
	"errors"
	"image"
	"time"

	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/prop"

	// Register camera driver
	_ "github.com/pion/mediadevices/pkg/driver/camera"
)

// DetectionResult is the output for a single frame.
type DetectionResult struct {
	Faces []RecognizedFace
	// Frame is optional; included for debugging only. Implementations may leave it nil.
	Frame image.Image
}

type CameraConfig struct {
	// DeviceIndex for webcam (pion capture). Example: 0
	DeviceIndex int
	// Optional: frame width/height constraints.
	Width  int
	Height int
}

// RealtimeOptions configures the realtime recognition loop.
type RealtimeOptions struct {
	Camera CameraConfig
	// MatchThreshold controls known/unknown decision.
	MatchThreshold float64

	// FPS limits the frame processing rate. Zero means no limit (process every frame as fast as possible).
	FPS int

	// OnResult is called for every processed frame.
	OnResult func(ctx context.Context, res DetectionResult)

	// OnError is called when a frame fails (optional).
	OnError func(ctx context.Context, err error)
}

// RecognizerWithCamera extends FaceAI with camera support.
type RecognizerWithCamera interface {
	// RunRealtime starts capturing camera frames and running recognition continuously until ctx is done.
	RunRealtime(ctx context.Context, opts RealtimeOptions) error
}

// RunRealtime captures frames from the camera and processes them continuously.
func (m *memoryFaceAI) RunRealtime(ctx context.Context, opts RealtimeOptions) error {
	width := opts.Camera.Width
	if width <= 0 {
		width = 640
	}
	height := opts.Camera.Height
	if height <= 0 {
		height = 480
	}

	stream, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.Width = prop.Int(width)
			c.Height = prop.Int(height)
		},
	})
	if err != nil {
		return err
	}
	defer func() {
		for _, track := range stream.GetTracks() {
			track.Close()
		}
	}()

	videoTracks := stream.GetVideoTracks()
	if len(videoTracks) == 0 {
		return errors.New("no video track found")
	}

	track := videoTracks[0]
	vt, ok := track.(*mediadevices.VideoTrack)
	if !ok {
		return errors.New("track is not a video track")
	}

	reader := vt.NewReader(false)

	if opts.MatchThreshold > 0 {
		m.mu.Lock()
		m.cfg.MatchThreshold = opts.MatchThreshold
		m.mu.Unlock()
	}

	var frameInterval time.Duration
	if opts.FPS > 0 {
		frameInterval = time.Second / time.Duration(opts.FPS)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		frameStart := time.Now()

		img, release, err := reader.Read()
		if err != nil {
			if opts.OnError != nil {
				opts.OnError(ctx, err)
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}

		faces, recErr := m.RecognizeFrame(ctx, img)
		release()

		if recErr != nil {
			if opts.OnError != nil {
				opts.OnError(ctx, recErr)
			}
			continue
		}

		if opts.OnResult != nil {
			opts.OnResult(ctx, DetectionResult{
				Faces: faces,
				Frame: img,
			})
		}

		if frameInterval > 0 {
			elapsed := time.Since(frameStart)
			if elapsed < frameInterval {
				time.Sleep(frameInterval - elapsed)
			}
		}
	}
}
