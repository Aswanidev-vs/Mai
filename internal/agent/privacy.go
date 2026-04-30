package agent

import (
	"log"
	"strings"

	"github.com/user/mai/pkg/models"
)

// PrivacyGuard monitors data flow to prevent leaks to cloud providers
type PrivacyGuard struct {
	config models.Privacy
}

func NewPrivacyGuard(config models.Privacy) *PrivacyGuard {
	return &PrivacyGuard{config: config}
}

// IsSensitive checks if the text contains confidential information
func (g *PrivacyGuard) IsSensitive(text string) bool {
	if !g.config.DetectionEnabled {
		return false
	}

	lowerText := strings.ToLower(text)
	for _, word := range g.config.SensitiveWords {
		if strings.Contains(lowerText, strings.ToLower(word)) {
			log.Printf("[PRIVACY] Sensitive data detected: matched word '%s'", word)
			return true
		}
	}

	// Basic PII detection (emails, potential keys) - simplified for demo
	if strings.Contains(text, "@") && strings.Contains(text, ".") {
		log.Println("[PRIVACY] Potential email address detected.")
		return true
	}

	return false
}

// ShouldUseLocal returns true if the request must stay local
func (g *PrivacyGuard) ShouldUseLocal(text string, hybridMode bool) bool {
	if !hybridMode {
		return true // Always local if hybrid is off
	}
	return g.IsSensitive(text)
}
