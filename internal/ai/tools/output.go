package tools

import "github.com/Thadopz/telltale/internal/ai/evidence"

type ToolOutput struct {
	Success          bool                    `json:"success"`
	Status           string                  `json:"status,omitempty"`
	EvidenceID       string                  `json:"evidence_id,omitempty"`
	FailedQueryID    string                  `json:"failed_query_id,omitempty"`
	CanUseAsEvidence bool                    `json:"can_use_as_evidence,omitempty"`
	DataStatus       string                  `json:"data_status,omitempty"`
	Data             any                     `json:"data,omitempty"`
	ResultMetadata   evidence.ResultMetadata `json:"result_metadata,omitempty"`
	Subject          evidence.SubjectRef
	Scope            evidence.EvidenceScope
	Observations     []evidence.Observation
	Error            string `json:"error,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
	FailureReason    string `json:"failure_reason,omitempty"`
	Message          string `json:"message,omitempty"`
	Summary          string `json:"summary,omitempty"`
}

func marshalToolOutput(output ToolOutput) string {
	return evidence.Marshal(evidence.Envelope{
		Success:          output.Success,
		Status:           output.Status,
		EvidenceID:       output.EvidenceID,
		FailedQueryID:    output.FailedQueryID,
		CanUseAsEvidence: output.CanUseAsEvidence,
		DataStatus:       output.DataStatus,
		Data:             output.Data,
		ResultMetadata:   output.ResultMetadata,
		Subject:          output.Subject,
		Scope:            output.Scope,
		Observations:     output.Observations,
		Error:            output.Error,
		ErrorCode:        output.ErrorCode,
		FailureReason:    output.FailureReason,
		Message:          output.Message,
		Summary:          output.Summary,
	})
}

func marshalToolData(data any, message string) string {
	return evidence.Marshal(evidence.NewData(data, message))
}

func marshalToolEvidence(data any, message string, observations []evidence.Observation) string {
	return evidence.Marshal(evidence.NewDataWithObservations(data, message, observations))
}

func marshalToolError(err error, message string) string {
	return evidence.Marshal(evidence.NewError(err, message))
}
