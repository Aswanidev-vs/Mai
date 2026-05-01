package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type SkillRecord struct {
	Name      string    `json:"name"`
	Pattern   string    `json:"pattern"`
	Successes int       `json:"successes"`
	Failures  int       `json:"failures"`
	LastUsed  time.Time `json:"last_used"`
	CreatedAt time.Time `json:"created_at"`
}

type ProceduralStore struct {
	mu       sync.RWMutex
	skills   map[string]*SkillRecord
	filePath string
}

func NewProceduralStore(dataDir string) (*ProceduralStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create procedural dir: %w", err)
	}

	store := &ProceduralStore{
		skills:   make(map[string]*SkillRecord),
		filePath: filepath.Join(dataDir, "procedural.json"),
	}

	if err := store.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return store, nil
}

func (s *ProceduralStore) AddSkill(name string, pattern string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.skills[name]; ok {
		existing.Pattern = pattern
		existing.LastUsed = time.Now()
	} else {
		s.skills[name] = &SkillRecord{
			Name:      name,
			Pattern:   pattern,
			Successes: 0,
			Failures:  0,
			LastUsed:  time.Now(),
			CreatedAt: time.Now(),
		}
	}

	return s.save()
}

func (s *ProceduralStore) GetSkill(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	skill, ok := s.skills[name]
	if !ok {
		return "", fmt.Errorf("skill not found: %s", name)
	}

	skill.LastUsed = time.Now()
	return skill.Pattern, nil
}

func (s *ProceduralStore) RecordSuccess(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if skill, ok := s.skills[name]; ok {
		skill.Successes++
		skill.LastUsed = time.Now()
		s.save()
	}
}

func (s *ProceduralStore) RecordFailure(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if skill, ok := s.skills[name]; ok {
		skill.Failures++
		skill.LastUsed = time.Now()
		s.save()
	}
}

func (s *ProceduralStore) ListSkills() []SkillRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []SkillRecord
	for _, skill := range s.skills {
		list = append(list, *skill)
	}
	return list
}

func (s *ProceduralStore) GetBestPattern(taskType string) (string, float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var bestName string
	var bestScore float64 = -1

	for name, skill := range s.skills {
		if skill.Name == taskType || contains(skill.Pattern, taskType) {
			total := skill.Successes + skill.Failures
			if total == 0 {
				continue
			}
			score := float64(skill.Successes) / float64(total)
			if score > bestScore {
				bestScore = score
				bestName = name
			}
		}
	}

	if bestName != "" {
		return s.skills[bestName].Pattern, bestScore
	}
	return "", 0
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && len(substr) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func (s *ProceduralStore) save() error {
	data, err := json.MarshalIndent(s.skills, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *ProceduralStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.skills)
}
