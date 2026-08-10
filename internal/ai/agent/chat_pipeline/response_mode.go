package chat_pipeline

// ResponseMode controls whether the final answer must pass through the
// structured evidence pipeline.
type ResponseMode string

const (
	ResponseModeNormal     ResponseMode = "normal_chat"
	ResponseModeDiagnostic ResponseMode = "diagnostic"
)

func normalizeResponseMode(mode ResponseMode) ResponseMode {
	if mode == ResponseModeNormal {
		return ResponseModeNormal
	}
	return ResponseModeDiagnostic
}
