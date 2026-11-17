package trace

import "testing"

func TestCallbackAcceptsProviderEventsWithoutRunInfo(t *testing.T) {
	recorder := NewRecorder()
	handler := Callback("embedding", recorder)
	handler.OnStart(t.Context(), nil, nil)
	handler.OnEnd(t.Context(), nil, nil)
	if len(recorder.Steps()) != 0 {
		t.Fatal("unidentified event became a trace step")
	}
}
