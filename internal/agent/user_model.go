package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type UserPreference struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Confidence float64  `json:"confidence"`
	UpdatedAt time.Time `json:"updated_at"`
	Source    string    `json:"source"` // "explicit", "inferred", "observed"
}

type InteractionPattern struct {
	Action    string    `json:"action"`
	Count     int       `json:"count"`
	LastUsed  time.Time `json:"last_used"`
	TimeOfDay string    `json:"time_of_day"` // "morning", "afternoon", "evening", "night"
	DayOfWeek string    `json:"day_of_week"`
}

type UserProfile struct {
	Name         string            `json:"name"`
	PreferredName string           `json:"preferred_name"`
	Preferences  []UserPreference  `json:"preferences"`
	Patterns     []InteractionPattern `json:"patterns"`
	FirstSeen    time.Time         `json:"first_seen"`
	LastSeen     time.Time         `json:"last_seen"`
	TotalInteractions int          `json:"total_interactions"`
	FrequentApps []string          `json:"frequent_apps"`
	Topics       map[string]int    `json:"topics"` // topic -> mention count
}

type UserModel struct {
	mu       sync.RWMutex
	profile  *UserProfile
	filePath string
}

func NewUserModel(dataDir string) *UserModel {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("[UserModel] Failed to create dir: %v", err)
	}

	um := &UserModel{
		profile: &UserProfile{
			Preferences: make([]UserPreference, 0),
			Patterns:    make([]InteractionPattern, 0),
			Topics:      make(map[string]int),
			FirstSeen:   time.Now(),
		},
		filePath: filepath.Join(dataDir, "user_profile.json"),
	}

	um.load()
	return um
}

func (um *UserModel) RecordInteraction(text string, action string) {
	um.mu.Lock()
	defer um.mu.Unlock()

	um.profile.LastSeen = time.Now()
	um.profile.TotalInteractions++

	if action != "" {
		um.updatePattern(action)
	}

	um.extractTopics(text)
	um.save()
}

func (um *UserModel) SetPreference(key, value, source string) {
	um.mu.Lock()
	defer um.mu.Unlock()

	for i, pref := range um.profile.Preferences {
		if pref.Key == key {
			um.profile.Preferences[i].Value = value
			um.profile.Preferences[i].UpdatedAt = time.Now()
			um.profile.Preferences[i].Source = source
			um.save()
			return
		}
	}

	um.profile.Preferences = append(um.profile.Preferences, UserPreference{
		Key:       key,
		Value:     value,
		Confidence: 0.8,
		UpdatedAt: time.Now(),
		Source:    source,
	})
	um.save()
}

func (um *UserModel) GetPreference(key string) (string, bool) {
	um.mu.RLock()
	defer um.mu.RUnlock()

	for _, pref := range um.profile.Preferences {
		if pref.Key == key {
			return pref.Value, true
		}
	}
	return "", false
}

func (um *UserModel) RecordFrequentApp(app string) {
	um.mu.Lock()
	defer um.mu.Unlock()

	for i, a := range um.profile.FrequentApps {
		if a == app {
			um.profile.FrequentApps = append(um.profile.FrequentApps[:i], um.profile.FrequentApps[i+1:]...)
			break
		}
	}

	um.profile.FrequentApps = append([]string{app}, um.profile.FrequentApps...)
	if len(um.profile.FrequentApps) > 10 {
		um.profile.FrequentApps = um.profile.FrequentApps[:10]
	}
	um.save()
}

func (um *UserModel) GetContextString() string {
	um.mu.RLock()
	defer um.mu.RUnlock()

	var parts []string

	if um.profile.Name != "" {
		parts = append(parts, fmt.Sprintf("Name: %s", um.profile.Name))
	}

	if len(um.profile.Preferences) > 0 {
		var prefs []string
		for _, p := range um.profile.Preferences {
			prefs = append(prefs, fmt.Sprintf("%s: %s", p.Key, p.Value))
		}
		parts = append(parts, "Preferences: "+joinStrings(prefs, "; "))
	}

	if len(um.profile.FrequentApps) > 0 {
		apps := um.profile.FrequentApps
		if len(apps) > 5 {
			apps = apps[:5]
		}
		parts = append(parts, "Frequently used: "+joinStrings(apps, ", "))
	}

	topTopics := um.getTopTopics(5)
	if len(topTopics) > 0 {
		parts = append(parts, "Interested in: "+joinStrings(topTopics, ", "))
	}

	if um.profile.TotalInteractions > 0 {
		parts = append(parts, fmt.Sprintf("Interactions: %d since %s",
			um.profile.TotalInteractions, um.profile.FirstSeen.Format("Jan 2006")))
	}

	return joinStrings(parts, "\n")
}

func (um *UserModel) GetProfile() *UserProfile {
	um.mu.RLock()
	defer um.mu.RUnlock()
	return um.profile
}

func (um *UserModel) updatePattern(action string) {
	now := time.Now()
	timeOfDay := "morning"
	hour := now.Hour()
	switch {
	case hour >= 5 && hour < 12:
		timeOfDay = "morning"
	case hour >= 12 && hour < 17:
		timeOfDay = "afternoon"
	case hour >= 17 && hour < 21:
		timeOfDay = "evening"
	default:
		timeOfDay = "night"
	}

	for i, p := range um.profile.Patterns {
		if p.Action == action && p.TimeOfDay == timeOfDay {
			um.profile.Patterns[i].Count++
			um.profile.Patterns[i].LastUsed = now
			return
		}
	}

	um.profile.Patterns = append(um.profile.Patterns, InteractionPattern{
		Action:    action,
		Count:     1,
		LastUsed:  now,
		TimeOfDay: timeOfDay,
		DayOfWeek: now.Weekday().String(),
	})
}

func (um *UserModel) extractTopics(text string) {
	keywords := map[string][]string{
		"music":     {"play", "song", "music", "playlist", "album", "artist"},
		"movies":    {"movie", "film", "watch", "netflix", "series", "show"},
		"work":      {"meeting", "deadline", "project", "report", "email", "calendar"},
		"weather":   {"weather", "temperature", "rain", "sunny", "forecast"},
		"food":      {"food", "restaurant", "cook", "recipe", "dinner", "lunch", "breakfast"},
		"travel":    {"travel", "flight", "hotel", "trip", "vacation", "booking"},
		"shopping":  {"buy", "shop", "order", "price", "amazon", "store"},
		"health":    {"health", "exercise", "workout", "doctor", "medicine", "sleep"},
		"coding":    {"code", "program", "debug", "function", "api", "git", "compile"},
		"research":  {"search", "find", "look up", "research", "information", "what is"},
	}

	lower := text
	for topic, kws := range keywords {
		for _, kw := range kws {
			if strings.Contains(lower, kw) {
				um.profile.Topics[topic]++
				break
			}
		}
	}
}

func (um *UserModel) getTopTopics(n int) []string {
	type topicCount struct {
		topic string
		count int
	}

	var sorted []topicCount
	for topic, count := range um.profile.Topics {
		sorted = append(sorted, topicCount{topic, count})
	}

	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var result []string
	for i := 0; i < n && i < len(sorted); i++ {
		result = append(result, sorted[i].topic)
	}
	return result
}

func (um *UserModel) save() {
	data, err := json.MarshalIndent(um.profile, "", "  ")
	if err != nil {
		log.Printf("[UserModel] Failed to marshal: %v", err)
		return
	}
	if err := os.WriteFile(um.filePath, data, 0644); err != nil {
		log.Printf("[UserModel] Failed to save: %v", err)
	}
}

func (um *UserModel) load() {
	data, err := os.ReadFile(um.filePath)
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, um.profile); err != nil {
		log.Printf("[UserModel] Failed to load: %v", err)
	}
}

func joinStrings(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
