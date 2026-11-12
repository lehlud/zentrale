package datastore

import (
	"bytes"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestAttachmentBlobsSurviveRestartAndStayScopeBound(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "attachments.duckdb")
	store, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte{0, 255, 1, 0, 13, 10}
	a := domain.Attachment{ID: "same-id", AgentID: "main", ProjectID: "project", Name: "binary.dat", MimeType: "application/octet-stream", Size: int64(len(data)), Path: "/tmp/zentrale-main/attachments/same-id/binary.dat", MachineID: "machine"}
	if err := store.SaveAttachment(a, data); err != nil {
		t.Fatal(err)
	}
	receipt := domain.AgentInput{ID: "queued-receipt", AgentID: a.AgentID, ProjectID: a.ProjectID, Mode: "queue", Status: "queued", AttachmentIDs: []string{a.ID}, Attachments: []domain.Attachment{a}}
	if err := store.Save("agent_input", receipt.ID, receipt.AgentID, receipt); err != nil {
		t.Fatal(err)
	}
	other := a
	other.AgentID, other.ProjectID = "other", "other-project"
	if err := store.SaveAttachment(other, []byte("other")); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAttachment(a, []byte("changed")); err == nil {
		t.Fatal("mutable upload accepted")
	}
	if _, _, err := store.Attachment("unknown", a.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("foreign attachment exposed", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, blob, err := store.Attachment(a.AgentID, a.ID)
	if err != nil || loaded != a || !bytes.Equal(blob, data) {
		t.Fatal(loaded, blob, err)
	}
	inputs, err := store.AgentInputs(a.AgentID)
	if err != nil || len(inputs) != 1 || inputs[0].Status != "queued" || len(inputs[0].AttachmentIDs) != 1 || inputs[0].AttachmentIDs[0] != a.ID || len(inputs[0].Attachments) != 1 || inputs[0].Attachments[0] != a {
		t.Fatal("queued attachment receipt lost on restart", inputs, err)
	}
	if err := store.RemoveProject(a.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Attachment(a.AgentID, a.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("project blob orphan", err)
	}
	if loaded, blob, err := store.Attachment(other.AgentID, other.ID); err != nil || loaded != other || string(blob) != "other" {
		t.Fatal("other project blob removed", loaded, err)
	}
	if err := store.RemoveAgents([]string{other.AgentID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Attachment(other.AgentID, other.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("agent blob orphan", err)
	}
}
