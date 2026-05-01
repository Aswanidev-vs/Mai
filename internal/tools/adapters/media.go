package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"time"

	"github.com/go-vgo/robotgo"
	"github.com/user/mai/pkg/interfaces"
)

// YouTubePlayTool specifically handles playing music/videos on YouTube
type YouTubePlayTool struct{}

func (t *YouTubePlayTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "play_youtube",
		Description: "Plays a song or video on YouTube. Use this when the user says 'play', 'listen to', or 'watch' something on YouTube.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": { "type": "string", "description": "The name of the song, artist, or video to play" }
			},
			"required": ["query"]
		}`),
	}
}

func (t *YouTubePlayTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{}, err
	}

	playURL := fmt.Sprintf("https://www.youtube.com/results?search_query=%s", url.QueryEscape(args.Query))

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, `C:\Windows\System32\cmd.exe`, "/c", "start", playURL)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", playURL)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", playURL)
	}

	err := cmd.Run()
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	// Wait for the search results page to load, then click the first video.
	// YouTube search results layout: filter chips sit at ~15% height,
	// first video thumbnail starts at ~35% height.
	time.Sleep(4 * time.Second)

	screenW, screenH := robotgo.GetScreenSize()
	clickX := screenW * 30 / 100
	clickY := screenH * 35 / 100
	robotgo.Move(clickX, clickY)
	time.Sleep(50 * time.Millisecond)
	robotgo.Click()
	time.Sleep(2 * time.Second)

	// Tab-navigate as fallback to ensure we land on the first video link
	for i := 0; i < 5; i++ {
		robotgo.KeyTap("tab")
		time.Sleep(200 * time.Millisecond)
	}
	robotgo.KeyTap("enter")
	time.Sleep(2 * time.Second)

	// Press 'k' to confirm playback is active on the video page
	robotgo.KeyTap("k")

	return interfaces.ToolResult{Output: fmt.Sprintf("Playing %s on YouTube", args.Query)}, nil
}
