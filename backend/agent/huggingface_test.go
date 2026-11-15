package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

type datasetRoundTripper func(*http.Request) (*http.Response, error)

func (run datasetRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func TestDatasetDownloadUsesSSHJobWithoutCredentialExposure(t *testing.T) {
	engine, record, project := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("direct dataset download must not call inference")
		return providers.Result{}, nil
	}), shellPeerHandler("/bin/sh", os.Environ()))

	const token = "hf_DummySSHDownloadCredential12345"
	const content = "private fixture dataset\x00\n"
	engine.HuggingFace.Client.Transport = datasetRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Error("dataset account credential was not applied internally")
		}

		body := content
		if request.URL.Path == "/api/whoami-v2" {
			body = `{"type":"user","name":"dataset-owner"}`
		}

		return &http.Response{
			StatusCode:    200,
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       request,
		}, nil
	})
	if _, err := engine.HuggingFace.Connect(context.Background(), token); err != nil {
		t.Fatal(err)
	}

	arguments := map[string]any{
		"dataset": "org/private", "file": "train.parquet",
		"destination": "data/train.parquet", "maxBytes": float64(1024),
		"timeout_ms": float64(30000), "yield_time_ms": float64(10000),
	}
	result, err := engine.downloadDataset(context.Background(), record, arguments)
	if err != nil {
		t.Fatal(err)
	}

	var response struct {
		Job domain.Job `json:"job"`
	}
	if err := json.Unmarshal([]byte(JSON(result)), &response); err != nil || response.Job.Status != "completed" {
		t.Fatal("dataset job did not complete", JSON(result), err)
	}

	encoded, err := os.ReadFile(filepath.Join(project.Directory, "data/train.parquet"))
	if err != nil || string(encoded) != content {
		t.Fatal("file did not reach the project through SSH", string(encoded), err)
	}

	if strings.Contains(JSON(result), token) || strings.Contains(response.Job.Command, "python3") || !strings.Contains(response.Job.Command, "org/private") {
		t.Fatal("job exposed its source implementation or credential", JSON(result))
	}

	// Repeating an explicit call is not an implicit overwrite or resumed transfer.
	result, err = engine.downloadDataset(context.Background(), record, arguments)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal([]byte(JSON(result)), &response); err != nil || response.Job.Status != "failed" || !strings.Contains(response.Job.Output, "already exists") {
		t.Fatal("existing data was not preserved", JSON(result), err)
	}
}

func TestDatasetDownloadRejectsUnsafeArgumentsBeforeExecution(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("invalid download called inference")
		return providers.Result{}, nil
	}))

	for _, change := range []map[string]any{
		{"dataset": "https://other.invalid/data"},
		{"file": "a/../secret"},
		{"destination": "/tmp/outside"},
		{"destination": "a/../outside"},
		{"sha256": "invalid"},
		{"maxBytes": float64(1.5)},
		{"maxBytes": float64(0)},
		{"timeout_ms": nil},
	} {
		arguments := map[string]any{
			"dataset": "org/data", "file": "file", "destination": "data/file",
			"maxBytes": float64(100), "timeout_ms": float64(30000),
		}
		for key, value := range change {
			arguments[key] = value
		}

		if _, err := engine.downloadDataset(context.Background(), record, arguments); err == nil {
			t.Fatal("unsafe download arguments accepted", change)
		}
	}
}
