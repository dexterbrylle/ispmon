package outage

import (
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

// ActionKind represents state transitions produced by the outage state machine.
type ActionKind int

const (
	ActionNone ActionKind = iota
	ActionOpen
	ActionRestore
)

// Action describes what the engine must execute (store update and notifications).
type Action struct {
	Kind            ActionKind
	StartedAt       time.Time
	EndedAt         time.Time
	DurationSeconds int
	Reason          string
	EpisodeID       int64
}

// StateMachine is a pure state machine tracking consecutive probe failures and outage episodes.
type StateMachine struct {
	failRoundsRequired int
	consecutiveFails   int
	firstFailedAt      time.Time
	lastReason         string
	isOpen             bool
	openEpisodeID      int64
	openedAt           time.Time
}

// NewStateMachine creates a new outage StateMachine, optionally initializing from an existing active episode.
func NewStateMachine(failRoundsRequired int, activeEpisode *store.OutageEpisode) *StateMachine {
	if failRoundsRequired < 1 {
		failRoundsRequired = 1
	}
	sm := &StateMachine{
		failRoundsRequired: failRoundsRequired,
	}
	if activeEpisode != nil {
		sm.isOpen = true
		sm.openEpisodeID = activeEpisode.ID
		sm.openedAt = activeEpisode.StartedAt
		sm.consecutiveFails = failRoundsRequired
		sm.lastReason = activeEpisode.Reason
	}
	return sm
}

// IsOpen returns whether an outage episode is currently open.
func (sm *StateMachine) IsOpen() bool {
	return sm.isOpen
}

// SetActiveEpisodeID updates the active episode ID after persisting to the store.
func (sm *StateMachine) SetActiveEpisodeID(id int64) {
	sm.openEpisodeID = id
}

// Step advances the state machine with one round's result and returns an action if state changed.
func (sm *StateMachine) Step(roundOK bool, reason string, now time.Time) Action {
	now = now.UTC()
	if !roundOK {
		sm.consecutiveFails++
		if sm.consecutiveFails == 1 {
			sm.firstFailedAt = now
		}
		sm.lastReason = reason

		if sm.consecutiveFails >= sm.failRoundsRequired && !sm.isOpen {
			sm.isOpen = true
			sm.openedAt = sm.firstFailedAt
			return Action{
				Kind:      ActionOpen,
				StartedAt: sm.firstFailedAt,
				Reason:    sm.lastReason,
			}
		}

		return Action{Kind: ActionNone}
	}

	// Round is successful
	sm.consecutiveFails = 0
	sm.firstFailedAt = time.Time{}

	if sm.isOpen {
		sm.isOpen = false
		duration := int(now.Sub(sm.openedAt).Seconds())
		if duration < 0 {
			duration = 0
		}
		act := Action{
			Kind:            ActionRestore,
			StartedAt:       sm.openedAt,
			EndedAt:         now,
			DurationSeconds: duration,
			Reason:          sm.lastReason,
			EpisodeID:       sm.openEpisodeID,
		}
		sm.openEpisodeID = 0
		sm.openedAt = time.Time{}
		return act
	}

	return Action{Kind: ActionNone}
}
