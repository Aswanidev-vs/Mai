package personality

import (
	"log"
	"math"
	"sync"
	"time"
)

type ProsodyFeatures struct {
	RMSEnergy      float64 `json:"rms_energy"`
	ZeroCrossRate  float64 `json:"zero_cross_rate"`
	SpectralCentroid float64 `json:"spectral_centroid"`
	SpeechRate     float64 `json:"speech_rate"`
	PitchMean      float64 `json:"pitch_mean"`
	PitchVariance  float64 `json:"pitch_variance"`
	VolumeVariance float64 `json:"volume_variance"`
	PauseRatio     float64 `json:"pause_ratio"`
}

type ProsodyAnalyzer struct {
	mu            sync.RWMutex
	features      ProsodyFeatures
	history       []ProsodyFeatures
	emotionMap    map[EmotionType]ProsodyFeatures
	maxHistory    int
}

func NewProsodyAnalyzer() *ProsodyAnalyzer {
	pa := &ProsodyAnalyzer{
		history:    make([]ProsodyFeatures, 0, 100),
		maxHistory: 100,
		emotionMap: map[EmotionType]ProsodyFeatures{
			EmotionHappy: {
				RMSEnergy:      0.6,
				ZeroCrossRate:  0.4,
				SpectralCentroid: 0.7,
				SpeechRate:     0.6,
				PitchMean:      0.7,
				PitchVariance:  0.6,
				VolumeVariance: 0.4,
				PauseRatio:     0.2,
			},
			EmotionSad: {
				RMSEnergy:      0.2,
				ZeroCrossRate:  0.2,
				SpectralCentroid: 0.3,
				SpeechRate:     0.3,
				PitchMean:      0.3,
				PitchVariance:  0.2,
				VolumeVariance: 0.2,
				PauseRatio:     0.5,
			},
			EmotionStressed: {
				RMSEnergy:      0.8,
				ZeroCrossRate:  0.6,
				SpectralCentroid: 0.6,
				SpeechRate:     0.8,
				PitchMean:      0.6,
				PitchVariance:  0.8,
				VolumeVariance: 0.7,
				PauseRatio:     0.1,
			},
			EmotionExcited: {
				RMSEnergy:      0.8,
				ZeroCrossRate:  0.5,
				SpectralCentroid: 0.8,
				SpeechRate:     0.7,
				PitchMean:      0.8,
				PitchVariance:  0.7,
				VolumeVariance: 0.6,
				PauseRatio:     0.15,
			},
			EmotionFrustrated: {
				RMSEnergy:      0.7,
				ZeroCrossRate:  0.5,
				SpectralCentroid: 0.5,
				SpeechRate:     0.6,
				PitchMean:      0.4,
				PitchVariance:  0.7,
				VolumeVariance: 0.8,
				PauseRatio:     0.3,
			},
			EmotionCalm: {
				RMSEnergy:      0.3,
				ZeroCrossRate:  0.3,
				SpectralCentroid: 0.4,
				SpeechRate:     0.4,
				PitchMean:      0.5,
				PitchVariance:  0.2,
				VolumeVariance: 0.2,
				PauseRatio:     0.3,
			},
		},
	}
	return pa
}

func (pa *ProsodyAnalyzer) Analyze(samples []float32, sampleRate int) ProsodyFeatures {
	if len(samples) == 0 {
		return ProsodyFeatures{}
	}

	features := ProsodyFeatures{}

	features.RMSEnergy = pa.calculateRMS(samples)
	features.ZeroCrossRate = pa.calculateZeroCrossingRate(samples)
	features.SpectralCentroid = pa.calculateSpectralCentroid(samples, sampleRate)
	features.PitchMean = pa.estimatePitch(samples, sampleRate)
	features.VolumeVariance = pa.calculateVolumeVariance(samples)
	features.PauseRatio = pa.estimatePauseRatio(samples)
	features.SpeechRate = pa.estimateSpeechRate(samples, sampleRate)

	pa.mu.Lock()
	pa.features = features
	pa.history = append(pa.history, features)
	if len(pa.history) > pa.maxHistory {
		pa.history = pa.history[len(pa.history)-pa.maxHistory:]
	}
	pa.mu.Unlock()

	return features
}

func (pa *ProsodyAnalyzer) DetectEmotion(features ProsodyFeatures) EmotionState {
	bestEmotion := EmotionNeutral
	bestScore := math.MaxFloat64

	for emotion, template := range pa.emotionMap {
		score := pa.calculateDistance(features, template)
		if score < bestScore {
			bestScore = score
			bestEmotion = emotion
		}
	}

	confidence := 1.0 / (1.0 + bestScore)
	if confidence < 0.3 {
		bestEmotion = EmotionNeutral
		confidence = 0.5
	}

	arousal := pa.calculateArousal(features)
	valence := pa.calculateValence(features)

	state := EmotionState{
		Type:       bestEmotion,
		Confidence: confidence,
		Arousal:    arousal,
		Valence:    valence,
		Timestamp:  time.Now(),
		Source:     "prosody",
	}

	log.Printf("[Prosody] Detected: %s (confidence: %.2f, arousal: %.2f, valence: %.2f)",
		state.Type, state.Confidence, state.Arousal, state.Valence)

	return state
}

func (pa *ProsodyAnalyzer) GetFeatures() ProsodyFeatures {
	pa.mu.RLock()
	defer pa.mu.RUnlock()
	return pa.features
}

func (pa *ProsodyAnalyzer) calculateRMS(samples []float32) float64 {
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func (pa *ProsodyAnalyzer) calculateZeroCrossingRate(samples []float32) float64 {
	if len(samples) < 2 {
		return 0
	}
	crossings := 0
	for i := 1; i < len(samples); i++ {
		if (samples[i] >= 0 && samples[i-1] < 0) || (samples[i] < 0 && samples[i-1] >= 0) {
			crossings++
		}
	}
	return float64(crossings) / float64(len(samples)-1)
}

func (pa *ProsodyAnalyzer) calculateSpectralCentroid(samples []float32, sampleRate int) float64 {
	// Time-domain approximation of spectral centroid.
	// Computes an energy-weighted frequency estimate by dividing the spectrum
	// into bins and weighting by magnitude. This is a rough proxy — a proper
	// spectral centroid requires FFT-based frequency analysis.
	windowSize := 1024
	if len(samples) < windowSize {
		windowSize = len(samples)
	}

	var weightedSum, magnitudeSum float64
	for i := 0; i < windowSize; i++ {
		mag := math.Abs(float64(samples[i]))
		freq := float64(i*sampleRate) / float64(windowSize)
		weightedSum += freq * mag
		magnitudeSum += mag
	}

	if magnitudeSum == 0 {
		return 0.5 // neutral default
	}

	// Normalize by Nyquist frequency and scale to 0-1 range
	centroid := weightedSum / magnitudeSum
	normalized := centroid / (float64(sampleRate) / 2)
	if normalized > 1.0 {
		normalized = 1.0
	}
	return normalized
}

func (pa *ProsodyAnalyzer) estimatePitch(samples []float32, sampleRate int) float64 {
	if len(samples) < 2 {
		return 0
	}

	// Zero-crossing rate based pitch estimation.
	// This is a coarse approximation valid only for near-pure tones.
	// For real speech, an autocorrelation or cepstral method is preferred.
	var crossings int
	for i := 1; i < len(samples); i++ {
		if (samples[i] >= 0 && samples[i-1] < 0) || (samples[i] < 0 && samples[i-1] >= 0) {
			crossings++
		}
	}

	if crossings == 0 {
		return 0
	}

	freq := float64(crossings) * float64(sampleRate) / float64(2*len(samples))
	// Clamp to typical speech range (50-500 Hz), normalize to 0-1
	if freq < 50 {
		return 0.1
	}
	if freq > 500 {
		return 0.9
	}
	return (freq - 50) / 450.0
}

func (pa *ProsodyAnalyzer) estimateSpeechRate(samples []float32, sampleRate int) float64 {
	if len(samples) < sampleRate/2 {
		return 0.5 // default moderate rate for short segments
	}

	// Detect syllable-like energy peaks using envelope-based approach
	// Divide audio into short frames, count transitions above threshold
	frameSize := int(float64(sampleRate) * 0.01) // 10ms frames
	if frameSize < 1 {
		frameSize = 1
	}
	numFrames := len(samples) / frameSize
	if numFrames < 1 {
		return 0.5
	}

	// Compute RMS per frame
	frameEnergies := make([]float64, numFrames)
	for i := 0; i < numFrames; i++ {
		var sum float64
		start := i * frameSize
		for j := 0; j < frameSize && start+j < len(samples); j++ {
			s := float64(samples[start+j])
			sum += s * s
		}
		frameEnergies[i] = math.Sqrt(sum / float64(frameSize))
	}

	// Count energy peaks (syllable nuclei)
	peakCount := 0
	threshold := 0.02
	for i := 1; i < numFrames-1; i++ {
		if frameEnergies[i] > frameEnergies[i-1] && frameEnergies[i] > frameEnergies[i+1] && frameEnergies[i] > threshold {
			peakCount++
		}
	}

	// Estimate syllables per second, normalize to 0-1 range
	durationSec := float64(len(samples)) / float64(sampleRate)
	if durationSec <= 0 {
		return 0.5
	}
	syllablesPerSec := float64(peakCount) / durationSec

	// Map typical speech rates (2-7 syllables/sec) to 0-1
	rate := (syllablesPerSec - 1.0) / 7.0
	if rate < 0 {
		rate = 0
	}
	if rate > 1.0 {
		rate = 1.0
	}
	return rate
}

func (pa *ProsodyAnalyzer) calculateVolumeVariance(samples []float32) float64 {
	if len(samples) < 100 {
		return 0
	}

	chunkSize := len(samples) / 10
	var volumes []float64

	for i := 0; i < 10; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(samples) {
			end = len(samples)
		}

		var sum float64
		for _, s := range samples[start:end] {
			sum += float64(s) * float64(s)
		}
		volumes = append(volumes, math.Sqrt(sum/float64(chunkSize)))
	}

	mean := 0.0
	for _, v := range volumes {
		mean += v
	}
	mean /= float64(len(volumes))

	variance := 0.0
	for _, v := range volumes {
		variance += (v - mean) * (v - mean)
	}
	variance /= float64(len(volumes))

	return math.Min(1.0, variance*100)
}

func (pa *ProsodyAnalyzer) estimatePauseRatio(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}

	threshold := 0.01
	silentSamples := 0
	for _, s := range samples {
		if math.Abs(float64(s)) < threshold {
			silentSamples++
		}
	}

	return float64(silentSamples) / float64(len(samples))
}

func (pa *ProsodyAnalyzer) calculateDistance(a, b ProsodyFeatures) float64 {
	d := 0.0
	d += (a.RMSEnergy - b.RMSEnergy) * (a.RMSEnergy - b.RMSEnergy)
	d += (a.ZeroCrossRate - b.ZeroCrossRate) * (a.ZeroCrossRate - b.ZeroCrossRate)
	d += (a.SpectralCentroid - b.SpectralCentroid) * (a.SpectralCentroid - b.SpectralCentroid)
	d += (a.SpeechRate - b.SpeechRate) * (a.SpeechRate - b.SpeechRate)
	d += (a.PitchMean - b.PitchMean) * (a.PitchMean - b.PitchMean)
	d += (a.PitchVariance - b.PitchVariance) * (a.PitchVariance - b.PitchVariance)
	d += (a.VolumeVariance - b.VolumeVariance) * (a.VolumeVariance - b.VolumeVariance)
	d += (a.PauseRatio - b.PauseRatio) * (a.PauseRatio - b.PauseRatio)
	return math.Sqrt(d)
}

func (pa *ProsodyAnalyzer) calculateArousal(features ProsodyFeatures) float64 {
	return math.Min(1.0, (features.RMSEnergy*0.3+
		features.ZeroCrossRate*0.2+
		features.SpeechRate*0.2+
		features.PitchVariance*0.15+
		features.VolumeVariance*0.15))
}

func (pa *ProsodyAnalyzer) calculateValence(features ProsodyFeatures) float64 {
	valence := 0.5
	valence += (features.PitchMean - 0.5) * 0.3
	valence += (features.SpectralCentroid - 0.5) * 0.2
	if features.PauseRatio < 0.3 {
		valence += 0.1
	} else if features.PauseRatio > 0.5 {
		valence -= 0.1
	}
	return math.Max(0, math.Min(1, valence))
}
