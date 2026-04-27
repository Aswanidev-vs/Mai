package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// Vision handles on-demand screen understanding using local Vision LLMs.
type Vision struct {
	model string
	url   string
}

// NewVision creates a new Vision instance.
func NewVision(model, url string) *Vision {
	return &Vision{
		model: model,
		url:   url,
	}
}

// Close is a placeholder for interface compatibility.
func (v *Vision) Close() {}

// FindElement asks the Vision LLM to find the coordinates of an element.
func (v *Vision) FindElement(imagePath, description string) (int, int, error) {
	log.Printf("[VISION] Looking for %q using %s", description, v.model)

	// 1. Read and encode image
	imgData, err := os.ReadFile(imagePath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read image: %v", err)
	}
	base64Img := base64.StdEncoding.EncodeToString(imgData)

	// 2. Prepare prompt for coordinates
	// We ask for [x, y] coordinates in 0-1000 scale
	prompt := fmt.Sprintf("Act as a UI automation assistant. Locate the element: %s. Return only the center coordinates in the format [x, y] using a scale of 0 to 1000 for both axes.", description)

	// 3. Request from Ollama
	client := &http.Client{Timeout: 30 * time.Second}
	requestBody, _ := json.Marshal(map[string]interface{}{
		"model":  v.model,
		"prompt": prompt,
		"stream": false,
		"images": []string{base64Img},
	})

	resp, err := client.Post(v.url, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return 0, 0, fmt.Errorf("ollama vision post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("ollama vision error: %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, err
	}

	// 4. Parse [x, y] from response
	var xRel, yRel int
	_, err = fmt.Sscanf(result.Response, "[%d, %d]", &xRel, &yRel)
	if err != nil {
		// Try a more relaxed parse if model was chatty
		log.Printf("[VISION] Model response: %q. Failed to parse coordinates.", result.Response)
		return 0, 0, fmt.Errorf("could not parse coordinates from model response")
	}

	return xRel, yRel, nil
}
