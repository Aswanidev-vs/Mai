package faceai

import (
	"context"
	"errors"
	"image"
	"os"
	"sync"
	"time"
)

type memoryFaceAI struct {
	cfg Config

	mu       sync.RWMutex
	creators map[CreatorID]string
	// id -> embeddings (multiple per creator)
	embeddings map[CreatorID][][]float64

	// components (to be wired by build-tag implementations)
	detector Detector
	embedder Embedder

	// last-loaded/persisted schema (for consistency)
	storage *storageSchema
}

func New(cfg Config) (*memoryFaceAI, error) {
	if cfg.DataDir == "" {
		cfg.DataDir = defaultDataDir()
	}
	if cfg.MatchThreshold <= 0 {
		// stricter default; user can override
		cfg.MatchThreshold = 0.45
	}
	m := &memoryFaceAI{
		cfg:        cfg,
		creators:   map[CreatorID]string{},
		embeddings: map[CreatorID][][]float64{},
		detector:   nil,
		embedder:   nil,
		storage:    &storageSchema{Creators: map[CreatorID]creator{}, Embeddings: map[CreatorID][][]float64{}},
	}
	return m, nil
}

func (m *memoryFaceAI) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := loadFromDisk(m.cfg.DataDir)
	if err != nil {
		// If file doesn't exist yet, start empty.
		if os.IsNotExist(err) {
			m.creators = map[CreatorID]string{}
			m.embeddings = map[CreatorID][][]float64{}
			m.storage = &storageSchema{Creators: map[CreatorID]creator{}, Embeddings: map[CreatorID][][]float64{}}
			return nil
		}
		return err
	}

	m.creators = map[CreatorID]string{}
	m.embeddings = map[CreatorID][][]float64{}
	for id, c := range st.Creators {
		m.creators[id] = c.Name
	}
	for id, embs := range st.Embeddings {
		m.embeddings[id] = embs
	}
	m.storage = st
	return nil
}

func (m *memoryFaceAI) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	st := &storageSchema{
		Version:    1,
		Creators:   map[CreatorID]creator{},
		Embeddings: map[CreatorID][][]float64{},
	}
	for id, name := range m.creators {
		st.Creators[id] = creator{Name: name}
	}
	for id, embs := range m.embeddings {
		st.Embeddings[id] = embs
	}
	m.storage = st
	return saveToDisk(m.cfg.DataDir, st)
}

func (m *memoryFaceAI) SetPipeline(detector Detector, embedder Embedder) error {
	if detector == nil || embedder == nil {
		return errors.New("detector/embedder cannot be nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detector = detector
	m.embedder = embedder
	return nil
}

func (m *memoryFaceAI) EnrollFromImages(ctx context.Context, id CreatorID, name string, imgs []image.Image) error {
	if id == "" {
		return errors.New("id is empty")
	}
	if name == "" {
		return errors.New("name is empty")
	}
	if m.detector == nil || m.embedder == nil {
		return errors.New("face pipeline not configured (missing detector/embedder)")
	}
	if len(imgs) == 0 {
		return errors.New("no images provided")
	}

	var allEmbs [][]float64
	for _, img := range imgs {
		rects, err := m.detector.Detect(ctx, img)
		if err != nil {
			return err
		}
		if len(rects) == 0 {
			continue
		}

		// Enrollment: take first face crop
		face := cropOrFull(img, rects[0])
		emb, err := m.embedder.Embed(ctx, face)
		if err != nil {
			return err
		}
		allEmbs = append(allEmbs, emb)
	}

	if len(allEmbs) == 0 {
		return errors.New("no faces detected in enrollment images")
	}

	if m.cfg.EmbeddingPerEnrollment > 0 && len(allEmbs) > m.cfg.EmbeddingPerEnrollment {
		allEmbs = allEmbs[:m.cfg.EmbeddingPerEnrollment]
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.creators[id] = name
	m.embeddings[id] = append(m.embeddings[id], allEmbs...)
	return nil
}

func (m *memoryFaceAI) RecognizeFrame(ctx context.Context, frame image.Image) ([]RecognizedFace, error) {
	if m.detector == nil || m.embedder == nil {
		return nil, errors.New("face pipeline not configured (missing detector/embedder)")
	}
	rects, err := m.detector.Detect(ctx, frame)
	if err != nil {
		return nil, err
	}

	out := make([]RecognizedFace, 0, len(rects))
	now := time.Now()

	for _, r := range rects {
		face := cropOrFull(frame, r)
		emb, err := m.embedder.Embed(ctx, face)
		if err != nil {
			continue
		}

		var (
			bestID     CreatorID
			bestName   string
			bestDist   = 1e18
			secondDist = 1e18
			haveSecond bool
		)

		m.mu.RLock()
		for id, embs := range m.embeddings {
			for _, known := range embs {
				d, err := m.embedder.Distance(emb, known)
				if err != nil {
					continue
				}
				if d < bestDist {
					// When we displace the current best, we now have a real second candidate.
					haveSecond = haveSecond || bestDist < 1e17
					secondDist = bestDist
					bestDist = d
					bestID = id
					bestName = m.creators[id]
				} else if d < secondDist {
					secondDist = d
				}
			}
		}
		m.mu.RUnlock()

		rec := RecognizedFace{
			ID:       CreatorID(""),
			Name:     UnknownLabel,
			Distance: bestDist,
			BBox:     r,
			At:       now,
		}

		// More robust known/unknown:
		// Avoid relying on a single absolute "confidence" cutoff that other people can match.
		// Instead require strong *separation* between best and second-best.
		if bestName != "" && bestDist <= m.cfg.MatchThreshold && secondDist > 0 {
			// Require best match to be well inside threshold.
			bestWellBelow := bestDist <= (m.cfg.MatchThreshold * 0.70)

			// Absolute separation between best and 2nd best.
			minGap := 0.30 * m.cfg.MatchThreshold

			// Separation ratio cutoff.
			const minBestForRatio = 1e-6
			bestOk := (secondDist - bestDist) >= minGap

			ratioOk := false
			if bestDist < minBestForRatio {
				// Too tiny to ratio safely; rely on gap only.
				ratioOk = bestOk
			} else if bestOk {
				ratio := secondDist / bestDist
				ratioOk = ratio >= 1.40
			}

			// If 2nd best is still near threshold range, ambiguous => unknown.
			secondNear := secondDist <= (m.cfg.MatchThreshold * 1.05)

			// If we don't have a meaningful second-best, fallback to stricter best-only rule.
			if haveSecond {
				if bestWellBelow && bestOk && ratioOk && !secondNear {
					rec.ID = bestID
					rec.Name = bestName
				}
			} else {
				// No second-best evidence: accept only if extremely close and not ambiguous.
				if bestWellBelow && bestDist <= (m.cfg.MatchThreshold*0.55) {
					rec.ID = bestID
					rec.Name = bestName
				}
			}
		}

		out = append(out, rec)
	}

	return out, nil
}
