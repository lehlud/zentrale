package agent

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type trackedJobSource struct {
	io.Reader
	closed atomic.Int32
}

func (input *trackedJobSource) Close() error {
	input.closed.Add(1)
	if closer, ok := input.Reader.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}

func TestJobInputOpensLazilyAndClosesExactlyOnce(t *testing.T) {
	input := &trackedJobSource{Reader: strings.NewReader("data")}
	opened := 0
	open := func(ctx context.Context) (io.ReadCloser, error) {
		opened++
		return input, nil
	}
	if opened != 0 {
		t.Fatal("input opened before the job started")
	}

	output, err := runWithJobInput(context.Background(), nil, open, func(reader io.Reader) (string, error) {
		encoded, err := io.ReadAll(reader)
		return string(encoded), err
	})
	if err != nil || output != "data" || opened != 1 || input.closed.Load() != 1 {
		t.Fatal("input lifecycle failed", output, err, opened, input.closed.Load())
	}

	called := false
	_, err = runWithJobInput(context.Background(), nil, func(context.Context) (io.ReadCloser, error) {
		return nil, errors.New("approved source unavailable")
	}, func(io.Reader) (string, error) {
		called = true
		return "", nil
	})
	var inputFailure *jobInputError
	if called || !errors.As(err, &inputFailure) {
		t.Fatal("input failure launched a command or looked like an SSH failure", err)
	}
}

func TestJobInputCancellationUnblocksReader(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	input := &trackedJobSource{Reader: reader}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := runWithJobInput(ctx, nil, func(context.Context) (io.ReadCloser, error) {
			return input, nil
		}, func(source io.Reader) (string, error) {
			close(started)
			_, err := io.ReadAll(source)
			return "", err
		})
		done <- err
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		var inputFailure *jobInputError
		if !errors.As(err, &inputFailure) || input.closed.Load() != 1 {
			t.Fatal("cancelled input was not closed/classified correctly", err, input.closed.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled job input stayed blocked")
	}
}
