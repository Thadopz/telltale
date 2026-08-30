package eval

import (
	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// Evaluation helpers only transform or score an existing answer. Disabling
// provider-side reasoning keeps these small JSON calls fast and inexpensive;
// the diagnostic Agent itself retains its configured reasoning behavior.
func structuredEvaluationModelOptions() []model.Option {
	return []model.Option{
		openaiModel.WithExtraFields(map[string]any{
			"thinking":        map[string]string{"type": "disabled"},
			"response_format": map[string]string{"type": "json_object"},
		}),
		openaiModel.WithMaxCompletionTokens(2048),
	}
}
