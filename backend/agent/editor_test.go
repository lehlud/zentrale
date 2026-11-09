package agent

import (
	"context"
	"errors"
	"testing"
)

func TestEditorStartAfterShutdownDoesNotPrepareARemoteMachine(t *testing.T) {
	engine, record := testEngine(t, nil)
	engine.Close()
	_, err := engine.StartEditor(context.Background(), record.ProjectID, record.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("editor start after shutdown: %v", err)
	}
}
