package skills

import (
	"encoding/json"
	"os"
	"strings"
)

type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Triggers    []string `json:"triggers"`
	PromptSeed  string   `json:"prompt_seed"`
}

type Registry struct {
	skills   []Skill
	triggerM map[string]string // trigger(lower)->skillID
}

type Manifest struct {
	Skills []Skill `json:"skills"`
}

func LoadRegistry() *Registry {
	r := &Registry{
		triggerM: make(map[string]string),
	}

	// Optional: data/skills.json
	data, err := os.ReadFile("data/skills.json")
	if err == nil {
		var m Manifest
		if json.Unmarshal(data, &m) == nil && len(m.Skills) > 0 {
			r.skills = m.Skills
			r.index()
			return r
		}
	}

	// Hardcoded starter skills (v1)
	r.skills = []Skill{
		{
			ID:          "plan_day",
			Name:        "Plan My Day",
			Description: "Create a practical schedule for the user's day.",
			Triggers:    []string{"plan my day", "daily plan", "schedule my day", "what's my plan", "today schedule"},
			PromptSeed:  "You are a scheduling assistant. Create a concise, time-blocked plan.",
		},
		{
			ID:          "summarize",
			Name:        "Summarize",
			Description: "Summarize provided text or recent notes succinctly.",
			Triggers:    []string{"summarize", "summary", "tl;dr", "quick summary"},
			PromptSeed:  "Summarize the user's input in 5-8 bullet points. Include next actions if present.",
		},
		{
			ID:          "weekly_review",
			Name:        "Weekly Review",
			Description: "Perform a weekly review and propose improvements.",
			Triggers:    []string{"weekly review", "review my week", "weekly recap", "how did my week go"},
			PromptSeed:  "Do a weekly review and propose 3 improvements for next week. Keep it actionable.",
		},
	}

	r.index()
	return r
}

func (r *Registry) index() {
	for _, s := range r.skills {
		for _, t := range s.Triggers {
			tt := strings.ToLower(strings.TrimSpace(t))
			if tt == "" {
				continue
			}
			r.triggerM[tt] = s.ID
		}
	}
}

func (r *Registry) List() []Skill {
	return append([]Skill(nil), r.skills...)
}

func (r *Registry) Match(text string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	lower := strings.ToLower(text)

	// Simple trigger matching: if any trigger is contained in text.
	// (Can be upgraded to token scoring / fuzzy matching later.)
	for trigger, id := range r.triggerM {
		if strings.Contains(lower, trigger) {
			for _, s := range r.skills {
				if s.ID == id {
					return s, true
				}
			}
		}
	}
	return Skill{}, false
}
