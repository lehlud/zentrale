package agent

import (
	"context"
	"errors"
	"io"
	"sync"
)

const maximumQueuedInput = 64 * 1024

// The queue accepts each reviewed input atomically and bounds pending bytes.
type jobInput struct {
	mutex   sync.Mutex
	changed chan struct{}
	data    []byte
	eof     bool
	stopped bool
}

func newJobInput() *jobInput {
	return &jobInput{changed: make(chan struct{})}
}

func (input *jobInput) signalLocked() {
	close(input.changed)
	input.changed = make(chan struct{})
}

func (input *jobInput) send(ctx context.Context, data []byte, eof bool) error {
	input.mutex.Lock()
	defer input.mutex.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if input.stopped || input.eof {
			return errors.New("process input is closed")
		}

		if len(input.data)+len(data) <= maximumQueuedInput {
			input.data = append(input.data, data...)
			input.eof = eof
			input.signalLocked()

			return nil
		}

		changed := input.changed
		input.mutex.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
		}

		input.mutex.Lock()
	}
}

func (input *jobInput) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}

	input.mutex.Lock()
	defer input.mutex.Unlock()

	for {
		if input.stopped {
			return 0, io.ErrClosedPipe
		}

		if len(input.data) > 0 {
			count := copy(buffer, input.data)
			input.data = input.data[count:]
			input.signalLocked()

			return count, nil
		}

		if input.eof {
			return 0, io.EOF
		}

		changed := input.changed
		input.mutex.Unlock()
		<-changed
		input.mutex.Lock()
	}
}

func (input *jobInput) stop() {
	input.mutex.Lock()
	input.stopped = true
	input.data = nil
	input.signalLocked()
	input.mutex.Unlock()
}
