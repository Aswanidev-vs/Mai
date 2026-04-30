package main

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
)

// ActionType represents the type of automation action.
type ActionType string

const (
	ActionOpenApp    ActionType = "system.open_app"
	ActionCloseApp   ActionType = "system.close_app"
	ActionTypeText   ActionType = "system.type"
	ActionPressKey   ActionType = "system.key_press"
	ActionSendMsg    ActionType = "message.send"
	ActionMouseClick ActionType = "system.mouse_click"
	ActionMouseMove  ActionType = "system.mouse_move"
	ActionScreenshot ActionType = "system.screenshot"
	ActionWebSearch  ActionType = "web.search"
	ActionPlayMedia  ActionType = "media.play"
	ActionNone       ActionType = "none"
)

// Action represents a parsed automation command.
type Action struct {
	Type            ActionType             `json:"type"`
	Confidence      float64                `json:"confidence"`
	Params          map[string]interface{} `json:"params"`
	RequiresConfirm bool                   `json:"requires_confirmation"`
}

// ActionParser parses user text into structured actions.
type ActionParser struct {
	// rules contains regex patterns mapped to action types
	rules []parseRule
}

type parseRule struct {
	pattern    *regexp.Regexp
	actionType ActionType
	paramKeys  []string
	confidence float64
}

// NewActionParser creates a parser with built-in rules.
func NewActionParser() *ActionParser {
	return &ActionParser{
		rules: []parseRule{
			// Web search: "search interstellar on google", "look up weather on bing"
			{
				pattern:    regexp.MustCompile(`(?i)^(search|find|look\s*up)\s+(.+?)\s+(?:on|using)\s+(google|bing|yahoo|duckduckgo|youtube|wikipedia)(?:\s+(?:in|using|on)\s+(chrome|brave|firefox|edge))?`),
				actionType: ActionWebSearch,
				paramKeys:  []string{"_", "query", "platform", "browser"},
				confidence: 0.95,
			},
			// Play media: "play interstellar on youtube", "play some music on spotify"
			{
				pattern:    regexp.MustCompile(`(?i)^(play|listen\s*to)\s+(.+?)\s+(?:on|using)\s+(youtube|spotify|soundcloud)(?:\s+(?:in|using|on)\s+(chrome|brave|firefox|edge))?`),
				actionType: ActionPlayMedia,
				paramKeys:  []string{"_", "query", "platform", "browser"},
				confidence: 0.95,
			},
			// Combined: "open youtube and play interstellar"
			{
				pattern:    regexp.MustCompile(`(?i)^(?:open|go\s*to)\s+(youtube|spotify|soundcloud)\s+(?:and|to|for)\s+(?:play|search|find)\s+(.+?)(?:\s+(?:in|using|on)\s+(chrome|brave|firefox|edge))?$`),
				actionType: ActionPlayMedia,
				paramKeys:  []string{"platform", "query", "browser"},
				confidence: 0.95,
			},
			// Open site: "open youtube", "go to github.com"
			{
				pattern:    regexp.MustCompile(`(?i)^(open|go\s*to)\s+(youtube|google|github|facebook|twitter|instagram|reddit|amazon|netflix|gmail|wikipedia)(?:\s+(?:in|using|on)\s+(chrome|brave|firefox|edge))?`),
				actionType: ActionWebSearch,
				paramKeys:  []string{"_", "platform", "browser"},
				confidence: 0.90,
			},
			// Open application: "open chrome", "launch notepad", "start firefox"
			// Captures 1-3 words after the command to avoid grabbing repeated commands
			{
				pattern:    regexp.MustCompile(`(?i)^(open|launch|start)\s+([^\s]+(?:\s+[^\s]+){0,2})`),
				actionType: ActionOpenApp,
				paramKeys:  []string{"_", "name"},
				confidence: 0.95,
			},

			// Close application: "close notepad", "quit chrome"
			// Captures 1-3 words after the command to avoid grabbing repeated commands
			{
				pattern:    regexp.MustCompile(`(?i)^(close|quit|exit)\s+([^\s]+(?:\s+[^\s]+){0,2})`),
				actionType: ActionCloseApp,
				paramKeys:  []string{"_", "name"},
				confidence: 0.95,
			},

			// Type text: "type hello world", "write hello"
			{
				pattern:    regexp.MustCompile(`(?i)^(type|write)\s+(.+)$`),
				actionType: ActionTypeText,
				paramKeys:  []string{"_", "text"},
				confidence: 0.95,
			},
			// Press key: "press enter", "hit tab", "press ctrl+c"
			// Captures key name or combo (e.g., "ctrl+c", "alt+tab") but stops at next word
			{
				pattern:    regexp.MustCompile(`(?i)^(press|hit)\s+([^\s]+(?:\s*\+\s*[^\s]+)*)`),
				actionType: ActionPressKey,
				paramKeys:  []string{"_", "key"},
				confidence: 0.95,
			},

			// Send message to contact on app: "send hello to john on whatsapp", "sent hello to john on whatsapp"
			{
				pattern:    regexp.MustCompile(`(?i)^(?:send|sent|message|messaged)\s+(.+?)\s+to\s+(.+?)\s+on\s+(.+)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"text", "contact", "app"},
				confidence: 0.95,
			},
			// New Pattern: "find john on whatsapp and tell him hello"
			{
				pattern:    regexp.MustCompile(`(?i)^find\s+(.+?)\s+on\s+(\w+)\s+(?:and|to)\s+(?:tell|send|message)(?:\s+him|\s+her|\s+them)?\s+(.+)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"contact", "app", "text"},
				confidence: 0.98,
			},
			// New Pattern: "tell john on whatsapp hello"
			{
				pattern:    regexp.MustCompile(`(?i)^tell\s+(.+?)\s+on\s+(\w+)\s+(.+)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"contact", "app", "text"},
				confidence: 0.98,
			},
			// Alternative: "message john on whatsapp hello"
			{
				pattern:    regexp.MustCompile(`(?i)^(?:message|messaged)\s+(.+?)\s+on\s+(.+?)\s+(.+)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"contact", "app", "text"},
				confidence: 0.90,
			},

			// Simple send: "send hello to whatsapp", "message telegram hello"
			{
				pattern:    regexp.MustCompile(`(?i)^(?:send|sent|message|messaged)\s+(.+?)\s+to\s+(.+)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"text", "app"},
				confidence: 0.85,
			},
			// Alternative send: "message whatsapp hello world"
			{
				pattern:    regexp.MustCompile(`(?i)^message\s+(\w+)\s+(.+)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"app", "text"},
				confidence: 0.85,
			},
			// Mouse click: "click at 100 200", "click 500 300"
			{
				pattern:    regexp.MustCompile(`(?i)^click\s+(?:at\s+)?(\d+)\s+(\d+)$`),
				actionType: ActionMouseClick,
				paramKeys:  []string{"x", "y"},
				confidence: 0.95,
			},
			// Mouse move: "move mouse to 100 200", "move to 500 300"
			{
				pattern:    regexp.MustCompile(`(?i)^move\s+(?:mouse\s+)?(?:to\s+)?(\d+)\s+(\d+)$`),
				actionType: ActionMouseMove,
				paramKeys:  []string{"x", "y"},
				confidence: 0.90,
			},
			// Screenshot: "take screenshot", "capture screen"
			{
				pattern:    regexp.MustCompile(`(?i)^(take\s+screenshot|capture\s+screen)$`),
				actionType: ActionScreenshot,
				paramKeys:  []string{},
				confidence: 0.95,
			},
		},
	}
}

// Parse analyzes user text and returns an Action if a known pattern matches.
func (p *ActionParser) Parse(text string) Action {
	text = strings.TrimSpace(text)
	text = strings.ToLower(text)
	text = strings.TrimSuffix(text, ".") // Remove trailing period from ASR
	text = strings.TrimSuffix(text, "?")

	for _, rule := range p.rules {
		matches := rule.pattern.FindStringSubmatch(text)
		if matches == nil {
			continue
		}

		params := make(map[string]interface{})
		for i, key := range rule.paramKeys {
			if key == "_" {
				continue // Skip full match placeholder
			}
			if i+1 < len(matches) {
				params[key] = strings.TrimSpace(matches[i+1])
			}
		}

		return Action{
			Type:            rule.actionType,
			Confidence:      rule.confidence,
			Params:          params,
			RequiresConfirm: isDestructive(rule.actionType),
		}
	}

	return Action{Type: ActionNone, Confidence: 0.0}
}

// ParseFromAnywhere searches for action patterns anywhere in the text.
// It tries each sentence/phrase independently, since ASR may concatenate
// TTS output with the user's actual command.
func (p *ActionParser) ParseFromAnywhere(text string) Action {
	// 1. Try the normal anchored parse first (fast path)
	action := p.Parse(text)
	if action.Type != ActionNone {
		return action
	}

	// 2. Split into sentences and try each one
	// Use multiple delimiters: . ? !
	sentences := splitSentences(text)

	// Try from last to first — commands are typically at the end
	for i := len(sentences) - 1; i >= 0; i-- {
		action = p.Parse(sentences[i])
		if action.Type != ActionNone {
			return action
		}
	}

	return Action{Type: ActionNone, Confidence: 0.0}
}

// splitSentences splits text into sentence-like chunks.
func splitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// Split on sentence terminators
	var sentences []string
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '?' || r == '!'
	})

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			sentences = append(sentences, part)
		}
	}

	return sentences
}

// isDestructive returns true if the action could cause data loss or system changes.
func isDestructive(actionType ActionType) bool {
	switch actionType {
	case ActionCloseApp:
		return true
	default:
		return false
	}
}

// knownAppNames is a list of common app names for fuzzy matching.
var knownAppNames = []string{
	"chrome", "firefox", "edge", "notepad", "calculator",
	"whatsapp", "telegram", "discord", "spotify", "vscode",
	"terminal", "explorer", "cmd", "word", "excel", "powerpoint",
	"settings", "photos", "mail", "store", "paint", "brave",
}

// phoneticAliases maps common ASR misrecognitions to the correct app name.
// This is especially important for Indian English accents and similar variations.
var phoneticAliases = map[string]string{
	// WhatsApp variations
	"whats up":   "whatsapp",
	"what's up":  "whatsapp",
	"whats app":  "whatsapp",
	"what sap":   "whatsapp",
	"what's app": "whatsapp",
	"watsapp":    "whatsapp",
	"wats up":    "whatsapp",
	"whatapp":    "whatsapp",
	"whatsap":    "whatsapp",
	// Chrome variations
	"crome":  "chrome",
	"krome":  "chrome",
	"chrom":  "chrome",
	"google": "chrome",
	// Discord variations
	"dis cord":  "discord",
	"this cord": "discord",
	// Telegram variations
	"tele gram": "telegram",
	// VSCode variations
	"vs code":            "vscode",
	"v s code":           "vscode",
	"visual studio code": "vscode",
	// Notepad variations
	"note pad": "notepad",
	"not pad":  "notepad",
	// Calculator variations
	"calc":       "calculator",
	"calcualtor": "calculator",
	"calculater": "calculator",
	// Explorer variations
	"file explorer": "explorer",
	"files":         "explorer",
	// Spotify variations
	"spot ify": "spotify",
	"sportify": "spotify",
	"spotfy":   "spotify",
	// Settings
	"setting": "settings",
}

// levenshteinDistance calculates the edit distance between two strings.
func levenshteinDistance(s1, s2 string) int {
	if len(s1) < len(s2) {
		return levenshteinDistance(s2, s1)
	}
	if len(s2) == 0 {
		return len(s1)
	}
	previousRow := make([]int, len(s2)+1)
	for i := range previousRow {
		previousRow[i] = i
	}
	for i, c1 := range s1 {
		currentRow := make([]int, len(s2)+1)
		currentRow[0] = i + 1
		for j, c2 := range s2 {
			insertions := previousRow[j+1] + 1
			deletions := currentRow[j] + 1
			substitutions := previousRow[j]
			if c1 != c2 {
				substitutions++
			}
			currentRow[j+1] = minInt(insertions, minInt(deletions, substitutions))
		}
		previousRow = currentRow
	}
	return previousRow[len(s2)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// fuzzyMatchAppName tries to find the closest known app name.
// It first checks phonetic aliases (common ASR misrecognitions),
// then falls back to Levenshtein distance matching.
func fuzzyMatchAppName(heard string) string {
	heard = strings.ToLower(strings.TrimSpace(heard))

	// Step 1: Exact match against known names
	for _, app := range knownAppNames {
		if app == heard {
			return app
		}
	}

	// Step 2: Check phonetic aliases (handles ASR misrecognition)
	if alias, ok := phoneticAliases[heard]; ok {
		log.Printf("[ACTION] Phonetic alias matched %q -> %q", heard, alias)
		return alias
	}

	// Step 2b: Check if heard text contains a known alias as substring
	for alias, app := range phoneticAliases {
		if strings.Contains(heard, alias) {
			log.Printf("[ACTION] Phonetic substring matched %q (contains %q) -> %q", heard, alias, app)
			return app
		}
	}

	// Step 2c: Check if any known app name is a substring of what was heard
	for _, app := range knownAppNames {
		if strings.Contains(heard, app) {
			log.Printf("[ACTION] Substring matched %q (contains %q)", heard, app)
			return app
		}
	}

	// Step 3: Fuzzy match with Levenshtein distance
	bestMatch := heard
	bestScore := -1.0
	for _, app := range knownAppNames {
		dist := levenshteinDistance(heard, app)
		maxLen := len(heard)
		if len(app) > maxLen {
			maxLen = len(app)
		}
		// Score is similarity percentage (higher is better)
		score := (1.0 - float64(dist)/float64(maxLen)) * 100
		if score > bestScore && score > 50 { // At least 50% similar
			bestScore = score
			bestMatch = app
		}
	}

	if bestMatch != heard {
		log.Printf("[ACTION] Fuzzy matched %q -> %q (score: %.0f%%)", heard, bestMatch, bestScore)
	}

	return bestMatch
}

// ActionExecutor runs parsed actions using the Automation primitives.
type ActionExecutor struct {
	auto          *Automation
	parser        *ActionParser
	minConfidence float64
}

// NewActionExecutor creates an executor with the given automation instance.
func NewActionExecutor(auto *Automation) *ActionExecutor {
	return &ActionExecutor{
		auto:          auto,
		parser:        NewActionParser(),
		minConfidence: 0.70,
	}
}

// SetMinConfidence changes the threshold for automatic execution.
func (e *ActionExecutor) SetMinConfidence(c float64) {
	e.minConfidence = c
}

// ParseAndExecute parses text and immediately executes if confidence is high enough.
// Returns (executed bool, feedback string, err error).
func (e *ActionExecutor) ParseAndExecute(text string) (bool, string, error) {
	action := e.parser.ParseFromAnywhere(text)

	if action.Type == ActionNone {
		return false, "", nil
	}

	log.Printf("[ACTION] Parsed: type=%s confidence=%.2f params=%v",
		action.Type, action.Confidence, action.Params)

	// Check confidence threshold
	if action.Confidence < e.minConfidence {
		return false, fmt.Sprintf("I'm not confident enough to %s. Please confirm.", action.Type), nil
	}

	// Execute the action
	feedback, err := e.Execute(action)
	if err != nil {
		return true, "", err
	}

	return true, feedback, nil
}

// Execute runs a specific action and returns human-readable feedback.
func (e *ActionExecutor) Execute(action Action) (string, error) {
	switch action.Type {
	case ActionOpenApp:
		name, ok := action.Params["name"].(string)
		if !ok {
			return "", fmt.Errorf("missing app name")
		}
		// Fuzzy match to handle ASR misheard words due to accent
		name = fuzzyMatchAppName(name)
		if err := e.auto.OpenApp(name); err != nil {
			return "", err
		}
		return fmt.Sprintf("Opened %s.", name), nil

	case ActionCloseApp:
		name, ok := action.Params["name"].(string)
		if !ok {
			return "", fmt.Errorf("missing app name")
		}
		if err := e.auto.CloseApp(name); err != nil {
			return "", err
		}
		return fmt.Sprintf("Closed %s.", name), nil

	case ActionTypeText:
		text, ok := action.Params["text"].(string)
		if !ok {
			return "", fmt.Errorf("missing text to type")
		}
		if err := e.auto.TypeText(text); err != nil {
			return "", err
		}
		return "Typed.", nil

	case ActionPressKey:
		key, ok := action.Params["key"].(string)
		if !ok {
			return "", fmt.Errorf("missing key")
		}
		if err := e.auto.PressKey(key); err != nil {
			return "", err
		}
		return fmt.Sprintf("Pressed %s.", key), nil

	case ActionSendMsg:
		app, ok1 := action.Params["app"].(string)
		text, ok2 := action.Params["text"].(string)
		contact, _ := action.Params["contact"].(string) // Optional contact
		if !ok1 || !ok2 {
			return "", fmt.Errorf("missing app or text")
		}
		// Fuzzy match app name
		app = fuzzyMatchAppName(app)
		if err := e.auto.SendMessage(app, contact, text); err != nil {
			return "", err
		}
		if contact != "" {
			return fmt.Sprintf("Sent message to %s on %s.", contact, app), nil
		}
		return fmt.Sprintf("Sent message to %s.", app), nil

	case ActionMouseClick:
		x, ok1 := action.Params["x"].(string)
		y, ok2 := action.Params["y"].(string)
		if !ok1 || !ok2 {
			return "", fmt.Errorf("missing coordinates")
		}
		var xi, yi int
		fmt.Sscanf(x, "%d", &xi)
		fmt.Sscanf(y, "%d", &yi)
		if err := e.auto.MouseClick(xi, yi); err != nil {
			return "", err
		}
		return fmt.Sprintf("Clicked at %s, %s.", x, y), nil

	case ActionMouseMove:
		x, ok1 := action.Params["x"].(string)
		y, ok2 := action.Params["y"].(string)
		if !ok1 || !ok2 {
			return "", fmt.Errorf("missing coordinates")
		}
		var xi, yi int
		fmt.Sscanf(x, "%d", &xi)
		fmt.Sscanf(y, "%d", &yi)
		if err := e.auto.MouseMove(xi, yi); err != nil {
			return "", err
		}
		return fmt.Sprintf("Moved mouse to %s, %s.", x, y), nil

	case ActionScreenshot:
		if err := e.auto.TakeScreenshot("screen.png"); err != nil {
			return "", err
		}
		return "Screenshot saved as screen.png.", nil

	case ActionWebSearch:
		platform, _ := action.Params["platform"].(string)
		query, _ := action.Params["query"].(string)
		browser, _ := action.Params["browser"].(string)
		if platform == "" {
			return "", fmt.Errorf("missing platform")
		}
		if err := e.auto.WebSearch(platform, query, browser); err != nil {
			return "", err
		}
		if query != "" {
			return fmt.Sprintf("Searching for %s on %s.", query, platform), nil
		}
		return fmt.Sprintf("Opened %s.", platform), nil

	case ActionPlayMedia:
		platform, _ := action.Params["platform"].(string)
		query, _ := action.Params["query"].(string)
		browser, _ := action.Params["browser"].(string)
		if platform == "" || query == "" {
			return "", fmt.Errorf("missing platform or query")
		}
		if err := e.auto.PlayMedia(platform, query, browser); err != nil {
			return "", err
		}
		return fmt.Sprintf("Playing %s on %s.", query, platform), nil

	default:
		return "", fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// ToJSON serializes an action to JSON for LLM context.
func (a Action) ToJSON() string {
	b, _ := json.Marshal(a)
	return string(b)
}

// String returns a human-readable description of the action.
func (a Action) String() string {
	if a.Type == ActionNone {
		return "No action"
	}
	return fmt.Sprintf("%s (confidence: %.2f)", a.Type, a.Confidence)
}
