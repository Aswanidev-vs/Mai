package main

import (
	"path/filepath"
	"testing"
)

func TestOverlayProfilePathIsAbsolute(t *testing.T) {
	p := overlayProfilePath()
	if !filepath.IsAbs(p) {
		t.Fatalf("overlayProfilePath() = %q, want an absolute path", p)
	}
}

func TestClampToBounds(t *testing.T) {
	tests := []struct {
		name                     string
		x, y, w, h               int
		left, top, width, height int
		wantX, wantY             int
	}{
		{
			name: "left edge of a monitor left of primary",
			x:    -2000, y: 0, w: 320, h: 480,
			left: -1920, top: 0, width: 3840, height: 1080,
			wantX: -1920, wantY: 0,
		},
		{
			name: "bottom-right of virtual desktop",
			x:    2000, y: 1000, w: 320, h: 480,
			left: -1920, top: 0, width: 3840, height: 1080,
			wantX: 1600, wantY: 600,
		},
		{
			name: "monitor above primary",
			x:    0, y: -2000, w: 320, h: 480,
			left: -1920, top: -1080, width: 3840, height: 2160,
			wantX: 0, wantY: -1080,
		},
		{
			name: "window wider than the desktop",
			x:    50, y: 50, w: 200, h: 200,
			left: 0, top: 0, width: 100, height: 100,
			wantX: 0, wantY: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotX, gotY := clampToBounds(tc.x, tc.y, tc.w, tc.h, tc.left, tc.top, tc.width, tc.height)
			if gotX != tc.wantX || gotY != tc.wantY {
				t.Fatalf("clampToBounds() = (%d, %d), want (%d, %d)", gotX, gotY, tc.wantX, tc.wantY)
			}
		})
	}
}
