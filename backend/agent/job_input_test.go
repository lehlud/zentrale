package agent

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestReviewedInputQueueIsAtomicBoundedAndCancellable(t *testing.T) {
	input := newJobInput()
	original := make([]byte, maximumQueuedInput)

	for index := range original {
		original[index] = 'a'
	}

	if err := input.send(context.Background(), original, false); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if err := input.send(ctx, []byte("not accepted"), false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}

	read := make([]byte, maximumQueuedInput)
	if count, err := input.Read(read); err != nil ||
		count != len(original) ||
		string(read) != string(original) {
		t.Fatal(count, err)
	}

	if err := input.send(context.Background(), nil, true); err != nil {
		t.Fatal(err)
	}

	if _, err := input.Read(read); err != io.EOF {
		t.Fatal("rejected input leaked", err)
	}

	empty := newJobInput()
	done := make(chan error, 1)
	go func() {
		_, err := empty.Read(make([]byte, 1))
		done <- err
	}()
	empty.stop()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stopped input readable")
		}
	case <-time.After(time.Second):
		t.Fatal("reader remained blocked")
	}
}
