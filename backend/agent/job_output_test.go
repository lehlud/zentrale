package agent

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func TestJobCursorReadsUTF8AcrossStorageAndPageBoundaries(t *testing.T) {
	content := strings.Repeat("€", 200000) + "last diagnostic\n"
	var buffer remote.OutputBuffer
	_, _ = buffer.Write([]byte(content))
	window := buffer.Window()
	var result strings.Builder
	var offset uint64
	for offset < window.Total {
		text, next, omitted := readJobWindow(window, offset, 997, true)
		if next <= offset || omitted != 0 || !utf8.ValidString(text) {
			t.Fatal("broken output cursor", offset, next, omitted)
		}
		result.WriteString(text)
		offset = next
	}
	if result.String() != content {
		t.Fatal("UTF-8 was lost or replayed")
	}
	partial := remote.OutputWindow{Head: []byte{0xe2, 0x82}, Total: 2}
	if text, next, _ := readJobWindow(partial, 0, 256, false); text != "" || next != 0 {
		t.Fatal("incomplete live scalar consumed", text, next)
	}
}

func TestJobCursorReportsExpiredMiddleOnceAndKeepsFinalDiagnostic(t *testing.T) {
	var buffer remote.OutputBuffer
	_, _ = buffer.Write([]byte("startup\n" + strings.Repeat("x", 2*1024*1024) + "\nfinal failure\n"))
	window := buffer.Window()
	var offset, omitted uint64
	var result strings.Builder
	for offset < window.Total {
		text, next, gap := readJobWindow(window, offset, 64000, true)
		if next <= offset {
			t.Fatal("cursor stalled")
		}
		result.WriteString(text)
		offset = next
		omitted += gap
	}
	if omitted != window.Total-uint64(len(window.Head)+len(window.Tail)) || strings.Count(result.String(), "output bytes expired") != 1 || !strings.HasSuffix(result.String(), "final failure\n") {
		t.Fatal("lost gap or final failure", omitted)
	}
}

func TestJobReadPositionPersistsAndExplicitReplayDoesNotRewind(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("log reads must not call a model")
		return providers.Result{}, nil
	}))
	content := strings.Repeat("progress\n", 80) + "complete\n"
	job := domain.Job{ID: "cursor-job", AgentID: record.ID, Status: "completed", Output: content}
	var buffer remote.OutputBuffer
	_, _ = buffer.Write([]byte(content))
	_ = engine.Store.Save("job", job.ID, record.ID, job)
	_ = engine.Store.Save("job_output", job.ID, record.ID, jobOutputCheckpoint{ID: job.ID, Window: buffer.Window()})
	first, err := engine.readJobOutput(job, map[string]any{"max_output_bytes": float64(256)})
	if err != nil || len(first.Output) != 256 {
		t.Fatal(first, err)
	}
	restarted, err := New(engine.Store, engine.Provider)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	rest, err := restarted.readJobOutput(job, map[string]any{})
	if err != nil || first.Output+rest.Output != content {
		t.Fatal("restart replayed bytes", rest, err)
	}
	_, err = restarted.readJobOutput(job, map[string]any{"cursor": encodeJobCursor(job.ID, 0), "max_output_bytes": float64(256)})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := restarted.readJobOutput(job, map[string]any{})
	if err != nil || empty.Output != "" {
		t.Fatal("explicit replay rewound implicit cursor", empty, err)
	}
	var stored domain.Job
	_ = engine.Store.Load("job", job.ID, &stored)
	if stored.Output != content {
		t.Fatal("cursor consumed the UI log")
	}
	stale := job
	stale.Status = "running"
	latest, err := restarted.readJobOutput(stale, map[string]any{})
	if err != nil || latest.Status != "completed" {
		t.Fatal("stale running status survived process completion", latest, err)
	}
	if _, err := restarted.readJobOutput(job, map[string]any{"cursor": encodeJobCursor("another-job", 0)}); err == nil {
		t.Fatal("accepted another job's cursor")
	}
}
