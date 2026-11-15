package agent

import (
	"context"
	"errors"
	"io"
	"sync"
)

// jobInputError distinguishes a failed approved input source from an SSH/VM
// interruption. The command itself is never replayed in either case.
type jobInputError struct{ cause error }

func (err *jobInputError) Error() string { return err.cause.Error() }
func (err *jobInputError) Unwrap() error { return err.cause }

type checkedJobInput struct{ io.Reader }

func (input checkedJobInput) Read(buffer []byte) (int, error) {
	count, err := input.Reader.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return count, &jobInputError{cause: err}
	}

	return count, err
}

func runWithJobInput(
	ctx context.Context,
	fixed io.Reader,
	open func(context.Context) (io.ReadCloser, error),
	run func(io.Reader) (string, error),
) (string, error) {
	if open == nil {
		return run(fixed)
	}

	input, err := open(ctx)
	if err != nil {
		return "", &jobInputError{cause: err}
	}

	if input == nil {
		return "", &jobInputError{cause: errors.New("job input source is unavailable")}
	}
	var once sync.Once
	closeInput := func() { once.Do(func() { input.Close() }) }
	defer closeInput()
	stop := context.AfterFunc(ctx, closeInput)
	defer stop()

	return run(checkedJobInput{Reader: input})
}
