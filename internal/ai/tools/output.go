package tools

import "github.com/Thadopz/telltale/internal/ai/evidence"

func marshalToolData(data any, message string) string {
	return evidence.Marshal(evidence.NewData(data, message))
}

func marshalToolEvidence(data any, message string, observations []evidence.Observation) string {
	return evidence.Marshal(evidence.NewDataWithObservations(data, message, observations))
}

func marshalToolError(err error, message string) string {
	return evidence.Marshal(evidence.NewError(err, message))
}
