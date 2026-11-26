package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed scripts/public_search.py
var ddgsSearchScript string

func ddgsSearch(python string) func(context.Context, *http.Client, string) ([]webSearchResult, error) {
	return func(ctx context.Context, _ *http.Client, query string) ([]webSearchResult, error) {
		if !filepath.IsAbs(python) {
			return nil, errors.New("DDGS Python must be an absolute path")
		}

		command := exec.CommandContext(ctx, python, "-I", "-c", ddgsSearchScript)
		command.Dir = "/"
		command.Env = []string{"LANG=C.UTF-8"}
		command.Stdin = strings.NewReader(query)
		output, err := command.Output()
		if err != nil {
			return nil, errors.New("the DDGS search did not complete")
		}

		var results []webSearchResult
		if err := json.Unmarshal(output, &results); err != nil || results == nil {
			return nil, errors.New("DDGS did not return readable results")
		}

		return results, nil
	}
}
