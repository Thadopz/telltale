package chat_pipeline

import "github.com/cloudwego/eino/schema"

func toString(value any) string {
	text, _ := value.(string)
	return text
}

func toDocuments(value any) []*schema.Document {
	switch documents := value.(type) {
	case []*schema.Document:
		return documents
	case []schema.Document:
		result := make([]*schema.Document, 0, len(documents))
		for index := range documents {
			document := documents[index]
			result = append(result, &document)
		}
		return result
	case []any:
		result := make([]*schema.Document, 0, len(documents))
		for _, item := range documents {
			if document, ok := item.(*schema.Document); ok {
				result = append(result, document)
			}
			if document, ok := item.(schema.Document); ok {
				copyDocument := document
				result = append(result, &copyDocument)
			}
		}
		return result
	default:
		return nil
	}
}
