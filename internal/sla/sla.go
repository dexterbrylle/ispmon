package sla

import (
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

// Status represents the outcome of an SLA evaluation.
type Status string

const (
	StatusOK               Status = "ok"
	StatusBreach           Status = "breach"
	StatusInsufficientData Status = "insufficient_data"
)

// ActionKind represents state transitions for breach episodes.
type ActionKind int

const (
	ActionNone ActionKind = iota
	ActionOpenBreach
	ActionUpdateBreach
	ActionResolveBreach
)

// EvaluationResult contains metrics from evaluating checks over a rolling window.
type EvaluationResult struct {
	Status            Status
	ViolatingCount    int
	TotalValidCount   int
	ExpectedCount     int
	MinRequiredCount  int
	ViolationPct      float64
	WorstDownloadMbps *float64
	WorstUploadMbps   *float64
	BreachedChecks    []store.NetworkCheck
}

// EngineAction describes what the state machine must execute in the store and notifier.
type EngineAction struct {
	Kind           ActionKind
	Episode        *store.SLABreachEpisode
	EpisodeIDToEnd int64
	Result         *EvaluationResult
}

// EvaluationConfig parameterizes the pure SLA calculation.
type EvaluationConfig struct {
	PlanDownloadThreshold float64
	PlanUploadThreshold   float64
	SLAWindowHours        int
	SLAViolationPct       float64
	SLAMinCoverage        float64
	CheckIntervalMinutes  int
}

// IsBelowThreshold returns true if either download or upload falls below its contractual threshold.
func IsBelowThreshold(down, up float64, thresholdDown, thresholdUp float64) bool {
	return down < thresholdDown || up < thresholdUp
}

// Evaluate performs pure calculation of SLA status over checks within the rolling window.
// measurement_error and outage periods are excluded from the ratio.
// Documented note: Excluding failed checks shrinks coverage and may defer evaluation (intended).
func Evaluate(checks []store.NetworkCheck, now time.Time, cfg EvaluationConfig) EvaluationResult {
	expected := (cfg.SLAWindowHours * 60) / cfg.CheckIntervalMinutes
	if expected < 1 {
		expected = 1
	}

	minRequired := int(float64(expected) * cfg.SLAMinCoverage)
	if minRequired < 1 {
		minRequired = 1
	}

	cutoff := now.Add(-time.Duration(cfg.SLAWindowHours) * time.Hour)

	// Filter valid checks within window
	var validChecks []store.NetworkCheck
	for _, c := range checks {
		if c.Status == "ok" && c.DownloadMbps != nil && c.UploadMbps != nil {
			if !c.Timestamp.Before(cutoff) && !c.Timestamp.After(now) {
				validChecks = append(validChecks, c)
			}
		}
	}

	totalValid := len(validChecks)
	if totalValid < minRequired {
		return EvaluationResult{
			Status:           StatusInsufficientData,
			TotalValidCount:  totalValid,
			ExpectedCount:    expected,
			MinRequiredCount: minRequired,
		}
	}

	var (
		violatingChecks []store.NetworkCheck
		worstDown       *float64
		worstUp         *float64
	)

	for _, c := range validChecks {
		dl := *c.DownloadMbps
		ul := *c.UploadMbps
		if IsBelowThreshold(dl, ul, cfg.PlanDownloadThreshold, cfg.PlanUploadThreshold) {
			violatingChecks = append(violatingChecks, c)
			if worstDown == nil || dl < *worstDown {
				val := dl
				worstDown = &val
			}
			if worstUp == nil || ul < *worstUp {
				val := ul
				worstUp = &val
			}
		}
	}

	violatingCount := len(violatingChecks)
	violationPct := (float64(violatingCount) / float64(totalValid)) * 100.0

	// Breach if strictly greater than SLA_VIOLATION_PCT
	status := StatusOK
	if violationPct > cfg.SLAViolationPct {
		status = StatusBreach
	}

	return EvaluationResult{
		Status:            status,
		ViolatingCount:    violatingCount,
		TotalValidCount:   totalValid,
		ExpectedCount:     expected,
		MinRequiredCount:  minRequired,
		ViolationPct:      violationPct,
		WorstDownloadMbps: worstDown,
		WorstUploadMbps:   worstUp,
		BreachedChecks:    violatingChecks,
	}
}

// DecideAction computes the state transition based on current active episode and evaluation result.
func DecideAction(activeEpisode *store.SLABreachEpisode, eval EvaluationResult, now time.Time) EngineAction {
	if eval.Status == StatusInsufficientData {
		return EngineAction{Kind: ActionNone, Result: &eval}
	}

	if eval.Status == StatusBreach {
		if activeEpisode == nil {
			// No open episode and breach -> open episode and send ONE alert with evidence
			newEp := &store.SLABreachEpisode{
				StartedAt:           now.UTC(),
				WorstDownloadMbps:   eval.WorstDownloadMbps,
				WorstUploadMbps:     eval.WorstUploadMbps,
				ViolatingCheckCount: eval.ViolatingCount,
				TotalCheckCount:     eval.TotalValidCount,
				ViolationPct:        eval.ViolationPct,
			}
			return EngineAction{
				Kind:    ActionOpenBreach,
				Episode: newEp,
				Result:  &eval,
			}
		}

		// Open episode and still breach -> update counts silently
		updatedEp := *activeEpisode
		updatedEp.ViolatingCheckCount = eval.ViolatingCount
		updatedEp.TotalCheckCount = eval.TotalValidCount
		updatedEp.ViolationPct = eval.ViolationPct
		if eval.WorstDownloadMbps != nil {
			if updatedEp.WorstDownloadMbps == nil || *eval.WorstDownloadMbps < *updatedEp.WorstDownloadMbps {
				updatedEp.WorstDownloadMbps = eval.WorstDownloadMbps
			}
		}
		if eval.WorstUploadMbps != nil {
			if updatedEp.WorstUploadMbps == nil || *eval.WorstUploadMbps < *updatedEp.WorstUploadMbps {
				updatedEp.WorstUploadMbps = eval.WorstUploadMbps
			}
		}
		return EngineAction{
			Kind:    ActionUpdateBreach,
			Episode: &updatedEp,
			Result:  &eval,
		}
	}

	// eval.Status == StatusOK
	if activeEpisode != nil {
		// Open episode and no breach -> close it and send a resolved notice
		return EngineAction{
			Kind:           ActionResolveBreach,
			Episode:        activeEpisode,
			EpisodeIDToEnd: activeEpisode.ID,
			Result:         &eval,
		}
	}

	return EngineAction{Kind: ActionNone, Result: &eval}
}
