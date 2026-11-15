package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/huggingface"
	"github.com/lehlud/zentrale/remote"
)

//go:embed scripts/huggingface.py
var huggingFaceWriter string

type datasetDownloadArguments struct {
	huggingface.DownloadRequest
	Destination string `json:"destination"`
	SHA256      string `json:"sha256"`
}

func decodeDatasetArguments(arguments map[string]any, target any) error {
	encoded, err := json.Marshal(arguments)
	if err != nil || json.Unmarshal(encoded, target) != nil {
		return errors.New("invalid Hugging Face arguments")
	}

	return nil
}

func (engine *Engine) downloadDataset(ctx context.Context, record domain.Agent, arguments map[string]any) (any, error) {
	var input datasetDownloadArguments
	if err := decodeDatasetArguments(arguments, &input); err != nil {
		return nil, err
	}

	if input.Revision == "" {
		input.Revision = "main"
	}

	if err := huggingface.ValidateDataset(input.Dataset, input.Revision); err != nil {
		return nil, err
	}

	if !huggingface.ValidRelativePath(input.File, false) ||
		!huggingface.ValidRelativePath(input.Destination, false) ||
		!huggingface.ValidSHA256(input.SHA256) || input.MaxBytes < 1 {
		return nil, errors.New("literal dataset file/destination, positive integer maxBytes and optional SHA-256 are required")
	}

	timeout, err := commandTimeout(arguments)
	if err != nil {
		return nil, err
	}
	yield, err := outputInteger(arguments, "yield_time_ms", 10000, 1000, 30000)
	if err != nil {
		return nil, err
	}

	options := JSON(map[string]any{
		"destination": input.Destination,
		"maxBytes":    input.MaxBytes,
		"sha256":      strings.ToLower(input.SHA256),
	})
	command := "python3 -c " + remote.Quote(huggingFaceWriter) + " " + remote.Quote(options)
	display := fmt.Sprintf("Hugging Face dataset %s@%s: %s -> %s", input.Dataset, input.Revision, input.File, input.Destination)

	return engine.executeRemote(ctx, record, executionRequest{
		MachineID:      stringArg(arguments, "machineId"),
		Background:     boolArg(arguments, "background"),
		Command:        command,
		DisplayCommand: display,
		Timeout:        timeout,
		YieldTime:      time.Duration(yield) * time.Millisecond,
		OutputBytes:    4096,
		// HTTPS runs in the application; the writer needs no guest internet.
		OpenInput: func(jobContext context.Context) (io.ReadCloser, error) {
			return engine.HuggingFace.OpenDownload(jobContext, input.DownloadRequest)
		},
	})
}
