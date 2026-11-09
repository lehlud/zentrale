package agent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type jobCursor struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId,omitempty"`
	Offset  uint64 `json:"offset"`
}

type jobOutputCheckpoint struct {
	ID      string              `json:"id"`
	AgentID string              `json:"agentId"`
	Window  remote.OutputWindow `json:"window"`
}

type jobOutputResult struct {
	domain.Job
	NextCursor   string `json:"nextCursor"`
	TailCursor   string `json:"tailCursor,omitempty"`
	HasMore      bool   `json:"hasMore"`
	OmittedBytes uint64 `json:"omittedBytes,omitempty"`
}

func encodeJobCursor(jobID string, offset uint64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(JSON(jobCursor{ID: jobID, Offset: offset})))
}

func decodeJobCursor(jobID, encoded string) (uint64, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(data) > 1024 {
		return 0, errors.New("invalid job output cursor")
	}
	var cursor jobCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.ID != jobID {
		return 0, errors.New("cursor belongs to a different job")
	}
	return cursor.Offset, nil
}

// An expired middle is reported once before reading the retained tail.
func readJobWindow(window remote.OutputWindow, offset uint64, limit int, complete bool) (string, uint64, uint64) {
	var text strings.Builder
	var omitted uint64
	segments := []struct {
		start uint64
		data  []byte
	}{
		{0, window.Head},
		{window.Total - uint64(len(window.Tail)), window.Tail},
	}
	if window.Total == uint64(len(window.Head)+len(window.Tail)) {
		// A scalar may straddle the buffer's internal head/tail boundary.
		segments = segments[:1]
		segments[0].data = append(window.Head, window.Tail...)
	}
	for _, segment := range segments {
		end := segment.start + uint64(len(segment.data))
		if offset >= end {
			continue
		}
		if offset < segment.start {
			gap := segment.start - offset
			omitted += gap
			fmt.Fprintf(&text, "\n[%d output bytes expired]\n", gap)
			offset = segment.start
		}
		available := segment.data[offset-segment.start:]
		count := min(len(available), limit)
		if count < len(available) {
			for count > 0 && !utf8.RuneStart(available[count]) {
				count--
			}
		} else if !complete && end == window.Total && count > 0 {
			// Defer a split scalar until the next SSH write completes it.
			start := count - 1
			for start > 0 && !utf8.RuneStart(available[start]) {
				start--
			}
			if !utf8.FullRune(available[start:count]) {
				count = start
			}
		}
		text.WriteString(strings.ToValidUTF8(string(available[:count]), "�"))
		offset += uint64(count)
		limit -= count
		if count < len(available) || limit == 0 {
			break
		}
	}
	return text.String(), offset, omitted
}

func (engine *Engine) readJobOutput(job domain.Job, arguments map[string]any) (jobOutputResult, error) {
	limit, err := outputInteger(arguments, "max_output_bytes", 8000, 256, 64000)
	if err != nil {
		return jobOutputResult{}, err
	}
	var checkpoint jobOutputCheckpoint
	engine.mu.Lock()
	running := engine.jobs[job.ID]
	engine.mu.Unlock()
	if running != nil {
		running.mu.Lock()
		job = running.job
		if running.output != nil {
			checkpoint.Window = running.output.Window()
		}
		running.mu.Unlock()
	} else {
		// The process may have finished while an input review or wait was in
		// progress. Read its final status together with the persisted output.
		if err := engine.Store.Load("job", job.ID, &job); err != nil {
			return jobOutputResult{}, err
		}
		_ = engine.Store.Load("job_output", job.ID, &checkpoint)
	}
	if checkpoint.Window.Total == 0 && job.Output != "" {
		var buffer remote.OutputBuffer
		_, _ = buffer.Write([]byte(job.Output))
		checkpoint.Window = buffer.Window()
	}
	cursor := jobCursor{ID: job.ID}
	saved := jobCursor{ID: job.ID, AgentID: job.AgentID}
	_ = engine.Store.Load("job_cursor", job.ID, &saved)
	if encoded := stringArg(arguments, "cursor"); encoded != "" {
		cursor.Offset, err = decodeJobCursor(job.ID, encoded)
		if err != nil {
			return jobOutputResult{}, err
		}
	} else {
		cursor.Offset = min(saved.Offset, checkpoint.Window.Total)
	}
	if cursor.Offset > checkpoint.Window.Total {
		return jobOutputResult{}, errors.New("job output cursor is ahead of the recorded stream")
	}
	if boolArg(arguments, "tail") {
		tailStart := uint64(0)
		if checkpoint.Window.Total > uint64(limit) {
			tailStart = checkpoint.Window.Total - uint64(limit)
		}
		cursor.Offset = max(cursor.Offset, tailStart)
	}
	var omitted uint64
	job.Output, cursor.Offset, omitted = readJobWindow(checkpoint.Window, cursor.Offset, limit, job.Status != "running")
	saved.Offset = max(saved.Offset, cursor.Offset)
	saved.AgentID = job.AgentID
	if err := engine.Store.Save("job_cursor", job.ID, job.AgentID, saved); err != nil {
		return jobOutputResult{}, err
	}
	result := jobOutputResult{Job: job, NextCursor: encodeJobCursor(job.ID, cursor.Offset), HasMore: cursor.Offset < checkpoint.Window.Total, OmittedBytes: omitted}
	if result.HasMore {
		tailOffset := uint64(0)
		if checkpoint.Window.Total > uint64(limit) {
			tailOffset = checkpoint.Window.Total - uint64(limit)
		}
		result.TailCursor = encodeJobCursor(job.ID, tailOffset)
	}
	return result, nil
}
