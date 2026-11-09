package agent

import (
	"context"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) writeStdin(ctx context.Context, record domain.Agent, request interaction) (any, error) {
	arguments := request.Arguments
	id := stringArg(request.Arguments, "id")
	var job domain.Job
	if err := engine.Store.Load("job", id, &job); err != nil {
		return nil, err
	}

	if job.AgentID != record.ID {
		return nil, errors.New("job belongs to another agent")
	}

	chars := stringArg(request.Arguments, "chars")
	eof := boolArg(request.Arguments, "eof")

	if value, exists := arguments["chars"]; exists {
		if _, ok := value.(string); !ok {
			return nil, errors.New("chars must be a string")
		}
	}

	if value, exists := arguments["eof"]; exists {
		if _, ok := value.(bool); !ok {
			return nil, errors.New("eof must be a boolean")
		}
	}

	if len(chars) > 16000 || !noSecrets(chars) {
		return nil, errors.New("process input must be at most 16 KB and contain no Zentrale credentials")
	}

	wait, err := outputInteger(request.Arguments, "yield_time_ms", 1000, 0, 30000)
	if err != nil {
		return nil, err
	}

	if _, err := outputInteger(arguments, "max_output_bytes", 8000, 256, 64000); err != nil {
		return nil, err
	}

	if cursor := stringArg(arguments, "cursor"); cursor != "" {
		if _, err := decodeJobCursor(id, cursor); err != nil {
			return nil, err
		}
	}

	engine.mu.Lock()
	running := engine.jobs[id]
	wake := engine.wake[record.ID]
	engine.mu.Unlock()

	if chars != "" || eof {
		if running == nil || running.input == nil {
			return nil, errors.New("start a running command with interactive=true or tty=true before sending input")
		}

		if request.Network != running.execution.Network {
			return nil, errors.New("process input must use the running command's original network setting")
		}

		running.mu.Lock()
		recentInputs := append([]string(nil), running.inputHistory...)
		omitted := running.inputHistoryOmitted
		recentOutput, _ := outputPreview(running.job.Output, 4000)

		if cursor := stringArg(arguments, "cursor"); cursor != "" {
			offset, _ := decodeJobCursor(id, cursor)
			if running.output != nil && offset > running.output.Window().Total {
				running.mu.Unlock()
				return nil, errors.New("job output cursor is ahead of the recorded stream")
			}
		}

		running.mu.Unlock()
		// Never trust caller-supplied process metadata. Bind review to this exact
		// live instance, its complete command and original sandbox privileges.
		request.Arguments = map[string]any{
			"id":    id,
			"chars": chars,
			"eof":   eof,

			"runningCommand": map[string]any{
				"command":   running.execution.Command,
				"machineId": job.MachineID,

				"sandbox": running.sandbox,
				"network": running.execution.Network,

				"outsideSandbox": running.execution.OutsideSandbox,

				"guestRoot": running.execution.GuestRoot,
				"tty":       running.execution.TTY,

				"launchDirectory": running.project.Directory,

				"recentInputs":       recentInputs,
				"olderInputsOmitted": omitted,

				"recentOutput": recentOutput,
			},
		}

		if _, err := engine.approve(ctx, record, request); err != nil {
			return nil, err
		}

		engine.mu.Lock()
		current := engine.jobs[id]
		stopping := engine.stopRequested[record.ID]
		engine.mu.Unlock()

		if current != running || stopping {
			return nil, errors.New("process ended or was stopped while input was reviewed")
		}

		if err := engine.Remote.Connector.VerifyTrust(running.connection); err != nil {
			return nil, err
		}

		engine.invalidate(record.ID)
		data := []byte(chars)
		closeInput := eof

		if eof && running.execution.TTY {
			data = append(data, 4) // Terminal EOF is VEOF; the PTY stays attached.
			closeInput = false
		}

		inputContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := running.input.send(inputContext, data, closeInput)
		cancel()

		if err != nil {
			return nil, err
		}

		running.mu.Lock()
		entry := chars

		if eof {
			entry += " [EOF]"
		}

		running.inputHistory = append(running.inputHistory, entry)
		bytes := 0

		for _, value := range running.inputHistory {
			bytes += len(value)
		}

		for bytes > 32000 && len(running.inputHistory) > 1 {
			bytes -= len(running.inputHistory[0])
			running.inputHistory = running.inputHistory[1:]
			running.inputHistoryOmitted = true
		}

		running.mu.Unlock()
	}

	if running != nil && wait > 0 {
		running.mu.Lock()
		changed := running.changed
		running.mu.Unlock()
		timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
		defer timer.Stop()

		select {
		case <-running.done:
		case <-changed:
			// Coalesce one output burst without adding a model round per byte.
			quiet := time.NewTimer(100 * time.Millisecond)

			select {
			case <-quiet.C:
			case <-running.done:
			case <-ctx.Done():
			}

			quiet.Stop()
		case <-timer.C:
		case <-wake:
			return map[string]string{"status": "interrupted", "reason": "new user instruction"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Keep cursor/output settings from the caller, rather than review metadata.
	return engine.readJobOutput(job, arguments)
}
