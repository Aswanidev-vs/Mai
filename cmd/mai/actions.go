package main

import (
	"encoding/json"
	"errors"
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
			// Web search with platform: "search interstellar on google", "look up weather on bing"
			{
				pattern:    regexp.MustCompile(`(?i)^(search|find|look\s*up)\s+(.+?)\s+(?:on|using)\s+(google|bing|yahoo|duckduckgo|youtube|wikipedia)(?:\s+(?:in|using|on)\s+(chrome|brave|firefox|edge))?`),
				actionType: ActionWebSearch,
				paramKeys:  []string{"_", "query", "platform", "browser"},
				confidence: 0.95,
			},
			// Play media with browser first: "play interstellar on brave on youtube", "play perfect in brave on youtube"
			{
				pattern:    regexp.MustCompile(`(?i)^(play|listen\s*to)\s+(.+?)\s+(?:on|using|in)\s+(chrome|brave|firefox|edge)(?:\s+browser)?\s+(?:on|using|in)\s+(youtube|spotify|soundcloud)`),
				actionType: ActionPlayMedia,
				paramKeys:  []string{"_", "query", "browser", "platform"},
				confidence: 0.95,
			},
			// Play media: "play interstellar on youtube", "play some music on spotify", "play perfect in youtube on brave"
			{
				pattern:    regexp.MustCompile(`(?i)^(play|listen\s*to)\s+(.+?)\s+(?:on|using|in)\s+(youtube|spotify|soundcloud)(?:\s+(?:in|using|on)\s+(chrome|brave|firefox|edge))?`),
				actionType: ActionPlayMedia,
				paramKeys:  []string{"_", "query", "platform", "browser"},
				confidence: 0.95,
			},
			// Combined: "open youtube and play interstellar"
			{
				pattern:    regexp.MustCompile(`(?i)^(?:open|go\s*to)\s+(youtube|spotify|soundcloud)\s+(?:and|to|for)\s+(?:play|search|find)\s+(.+?)(?:\s+(?:in|using|on)\s+(chrome|brave|firefox|edge))?`),
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
			// Open application: "open chrome", "launch notepad", "start firefox", "open spotify in brave"
			{
				pattern:    regexp.MustCompile(`(?i)^(open|launch|start)\s+(.+?)(?:\s+(?:in|using|on)\s+(chrome|brave|firefox|edge))?$`),
				actionType: ActionOpenApp,
				paramKeys:  []string{"_", "name", "browser"},
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

			// "send a message to Priya in whatsapp" / "... on whatsapp saying hello".
			// The recipient is captured from this text; there is no default.
			// This rule intentionally consumes "a message" as a placeholder, not
			// as the body. Execute will ask for the real text.
			{
				pattern:    regexp.MustCompile(`(?i)^(?:send|sent)\s+(?:a\s+)?(?:message|text)\s+to\s+(.+?)\s+(?:on|in|via)\s+(\w+)(?:\s+(?:saying|that|says)\s+(.+))?$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"contact", "app", "text"},
				confidence: 0.98,
			},
			// "send hello to mom in whatsapp", "sent Hello World to Priya on whatsapp".
			{
				pattern:    regexp.MustCompile(`(?i)^(?:send|sent)\s+(.+?)\s+to\s+(.+?)\s+(?:on|in|via)\s+(\w+)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"text", "contact", "app"},
				confidence: 0.96,
			},
			// "find John on whatsapp and tell him hello"
			{
				pattern:    regexp.MustCompile(`(?i)^find\s+(.+?)\s+(?:on|in|via)\s+(\w+)\s+(?:and|to)\s+(?:tell|send|message)(?:\s+him|\s+her|\s+them)?\s+(.+)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"contact", "app", "text"},
				confidence: 0.98,
			},
			// "tell John on whatsapp hello" (text may be omitted; Execute asks)
			{
				pattern:    regexp.MustCompile(`(?i)^tell\s+(.+?)\s+(?:on|in|via)\s+(\w+)(?:\s+(?:saying|that|says))?\s*(.*)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"contact", "app", "text"},
				confidence: 0.94,
			},
			// "message Priya on whatsapp hello" (text may be omitted)
			{
				pattern:    regexp.MustCompile(`(?i)^(?:message|messaged)\s+(.+?)\s+(?:on|in|via)\s+(\w+)(?:\s+(?:saying|that|says))?\s*(.*)$`),
				actionType: ActionSendMsg,
				paramKeys:  []string{"contact", "app", "text"},
				confidence: 0.94,
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
	// Keep the original case: message bodies must not be lowercased. All
	// patterns use (?i), and fuzzyMatchAppName lowercases only the app name.
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

	// Split on sentence terminators and common command conjunctions
	var sentences []string

	// First split by punctuation
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '?' || r == '!' || r == ';'
	})

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Sub-split by common conjunctions like " and " or " then "
		// which often separate multiple commands in a single ASR stream.
		subParts := strings.Split(part, " and ")
		for _, sp := range subParts {
			sp = strings.TrimSpace(sp)
			if sp != "" {
				sentences = append(sentences, sp)
			}
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

// isPlaceholderMessage rejects the filler phrases that a parser can capture
// when the user has not actually supplied a message body.
func isPlaceholderMessage(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Trim(normalized, " .,!?\"'")
	switch normalized {
	case "", "a message", "message", "text", "msg", "something":
		return true
	default:
		return false
	}
}

// pendingMessageIntent holds a partially specified message command while Mai
// asks the user for the missing contact or body.
type pendingMessageIntent struct {
	App     string
	Contact string
	Text    string
	Need    string // "contact" or "text"
}

// ActionExecutor runs parsed actions using the Automation primitives.
type ActionExecutor struct {
	auto          *Automation
	parser        *ActionParser
	minConfidence float64
	pending       *pendingMessageIntent
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
//
// This is the primary path to the WhatsApp message workflow. It is called from
// a user turn only (voice ASR or typed chat -> HandleInput -> DirectAction).
// The LLM whatsapp_send fallback calls Automation.PrepareMessage directly, but
// that method also requires an active user turn, so neither startup, timers,
// nor proactive tasks can open WhatsApp. The recipient and body are always
// derived from text; no contact is defaulted or hardcoded.
func (e *ActionExecutor) ParseAndExecute(text string) (bool, string, error) {
	// A prepared message or a missing contact/body takes priority over parsing
	// the next utterance as a fresh command. This is what makes the
	// prepare → confirm workflow conversational.
	if e.pending != nil {
		// Do not swallow an unrelated command as a message body. If the new
		// utterance is itself a recognized non-message action, abandon the
		// half-finished message and execute the new command instead.
		if parsed := e.parser.ParseFromAnywhere(text); parsed.Type != ActionNone && parsed.Type != ActionSendMsg {
			e.pending = nil
		} else {
			return e.handlePendingMessage(text)
		}
	}
	if e.auto != nil && e.auto.HasPendingSend() {
		return e.handleSendConfirmation(text)
	}

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

func (e *ActionExecutor) handlePendingMessage(text string) (bool, string, error) {
	input := strings.TrimSpace(text)
	if isCancelPhrase(input) {
		e.pending = nil
		return true, "Okay, I won't send anything.", nil
	}
	if input == "" {
		return true, "I still need a little more detail.", nil
	}

	intent := e.pending
	switch intent.Need {
	case "contact":
		contact := strings.Trim(input, "\"'")
		if isPlaceholderMessage(contact) {
			return true, fmt.Sprintf("Who should I message on %s?", intent.App), nil
		}
		intent.Contact = contact
		if isPlaceholderMessage(intent.Text) {
			intent.Need = "text"
			e.pending = intent
			return true, fmt.Sprintf("What should I say to %s on %s?", contact, intent.App), nil
		}
		return e.preparePendingMessage(intent)
	case "text":
		if isPlaceholderMessage(input) {
			return true, fmt.Sprintf("What should I say to %s on %s?", intent.Contact, intent.App), nil
		}
		intent.Text = input
		return e.preparePendingMessage(intent)
	default:
		e.pending = nil
		return false, "", nil
	}
}

func (e *ActionExecutor) preparePendingMessage(intent *pendingMessageIntent) (bool, string, error) {
	e.pending = nil
	if err := e.auto.PrepareMessage(intent.App, intent.Contact, intent.Text); err != nil {
		switch {
		case errors.Is(err, ErrContactNotFound):
			return true, fmt.Sprintf("I couldn't find %s in %s, so I didn't send anything.", intent.Contact, intent.App), nil
		case errors.Is(err, ErrAppNotFound):
			return true, "I couldn't open WhatsApp, so I didn't send anything.", nil
		default:
			return true, "", err
		}
	}
	return true, fmt.Sprintf(
		"I've opened %s's chat on %s and typed your message. Say \"send\" to send it, or \"cancel\" to discard it.",
		intent.Contact, intent.App,
	), nil
}

func (e *ActionExecutor) handleSendConfirmation(text string) (bool, string, error) {
	input := strings.ToLower(strings.TrimSpace(text))
	if isAffirmative(input) {
		if err := e.auto.ConfirmSend(); err != nil {
			switch {
			case errors.Is(err, ErrContactNotFound):
				return true, "The chat is no longer open, so I didn't send the message.", nil
			case errors.Is(err, ErrAppNotFound):
				return true, "WhatsApp is no longer open, so I didn't send the message.", nil
			default:
				return true, "", err
			}
		}
		return true, "Sent.", nil
	}
	if isNegative(input) {
		e.auto.CancelSend()
		return true, "Okay, I didn't send it. The draft is still in the chat if you want to edit it.", nil
	}
	return true, "Say \"send\" to send it, or \"cancel\" to discard it.", nil
}

func normalizePhrase(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.ReplaceAll(text, ",", " ")
	text = strings.Trim(text, " .,!?")
	return strings.Join(strings.Fields(text), " ")
}

func isAffirmative(text string) bool {
	text = normalizePhrase(text)
	switch text {
	case "yes", "yeah", "yep", "yup", "sure", "ok", "okay", "send", "send it",
		"confirm", "go ahead", "do it", "please send", "send please", "yes please":
		return true
	}
	return strings.HasPrefix(text, "yes ") || strings.HasPrefix(text, "yeah ") ||
		strings.HasPrefix(text, "ok ") || strings.HasPrefix(text, "okay ")
}

func isNegative(text string) bool {
	text = normalizePhrase(text)
	switch text {
	case "no", "nope", "cancel", "stop", "discard", "don't send", "do not send",
		"never mind", "nevermind", "forget it":
		return true
	}
	return strings.HasPrefix(text, "no ") || strings.HasPrefix(text, "don't ")
}

func isCancelPhrase(text string) bool {
	text = normalizePhrase(text)
	switch text {
	case "cancel", "stop", "never mind", "nevermind", "forget it", "no":
		return true
	default:
		return false
	}
}

// Execute runs a specific action and returns human-readable feedback.
func (e *ActionExecutor) Execute(action Action) (string, error) {
	switch action.Type {
	case ActionOpenApp:
		name, ok := action.Params["name"].(string)
		if !ok {
			return "", fmt.Errorf("missing app name")
		}
		browser, _ := action.Params["browser"].(string)
		// Fuzzy match to handle ASR misheard words due to accent
		name = fuzzyMatchAppName(name)
		if err := e.auto.OpenAppWithBrowser(name, browser); err != nil {
			return "", err
		}
		if browser != "" {
			return fmt.Sprintf("Opened %s in %s.", name, browser), nil
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
		text, _ := action.Params["text"].(string)
		// The recipient is exactly what the parser captured from the user's
		// utterance. There is intentionally no fallback contact.
		contact, _ := action.Params["contact"].(string)
		if !ok1 || strings.TrimSpace(app) == "" {
			return "", fmt.Errorf("missing app name")
		}
		app = fuzzyMatchAppName(app)
		if app != "whatsapp" {
			return "I can choreograph WhatsApp messages right now. Which contact should I message on WhatsApp?", nil
		}
		if strings.TrimSpace(contact) == "" {
			e.pending = &pendingMessageIntent{App: app, Text: text, Need: "contact"}
			return "Who should I message on WhatsApp?", nil
		}
		if isPlaceholderMessage(text) {
			e.pending = &pendingMessageIntent{App: app, Contact: contact, Need: "text"}
			return fmt.Sprintf("What should I say to %s on WhatsApp?", contact), nil
		}
		if err := e.auto.PrepareMessage(app, contact, text); err != nil {
			switch {
			case errors.Is(err, ErrContactNotFound):
				return fmt.Sprintf("I couldn't find %s in WhatsApp, so I didn't send anything.", contact), nil
			case errors.Is(err, ErrAppNotFound):
				return "I couldn't open WhatsApp, so I didn't send anything.", nil
			default:
				return "", err
			}
		}
		return fmt.Sprintf(
			"I've opened %s's chat on WhatsApp and typed your message. Say \"send\" to send it, or \"cancel\" to discard it.",
			contact,
		), nil

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
			platform = "google"
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
