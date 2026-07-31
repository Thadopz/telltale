package watcher

import "time"

func IsAbnormal(rule Rule, snapshot Snapshot) bool {
	if rule.MinimumSamples > 0 && snapshot.SampleCount < rule.MinimumSamples {
		return false
	}
	switch rule.Operator {
	case OperatorLessThan:
		return snapshot.Value < rule.Threshold
	case OperatorGreaterThan, OperatorCountAbove:
		return snapshot.Value > rule.Threshold
	default:
		return false
	}
}

func ApplySnapshot(state DetectorState, rule Rule, snapshot Snapshot) (DetectorState, string) {
	if state.State == "" {
		state.State = StateNormal
	}
	state.RuleID = rule.ID
	state.LastValue = snapshot.Value
	state.LastEvidenceID = snapshot.EvidenceID
	state.LastError = ""
	state.UpdatedAt = snapshot.CollectedAt

	if IsAbnormal(rule, snapshot) {
		state.ConsecutiveFailures++
		state.ConsecutiveSuccesses = 0
		if state.State != StateFiring && state.ConsecutiveFailures >= rule.FiringWindows {
			state.State = StateFiring
			return state, TransitionOpened
		}
		return state, TransitionNone
	}

	state.ConsecutiveFailures = 0
	state.ConsecutiveSuccesses++
	if state.State == StateFiring && state.ConsecutiveSuccesses >= rule.RecoveryWindows {
		state.State = StateNormal
		return state, TransitionResolved
	}
	return state, TransitionNone
}

func RecordCollectionError(state DetectorState, ruleID string, err error, now time.Time) DetectorState {
	if state.State == "" {
		state.State = StateNormal
	}
	state.RuleID = ruleID
	state.UpdatedAt = now
	if err != nil {
		state.LastError = err.Error()
	}
	return state
}
