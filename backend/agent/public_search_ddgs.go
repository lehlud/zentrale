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

func ddgsSearch(python string, retryWithoutQuotes bool) func(context.Context, *http.Client, string) ([]webSearchResult, error) {
	return func(ctx context.Context, _ *http.Client, query string) ([]webSearchResult, error) {
		if !filepath.IsAbs(python) {
			return nil, errors.New("DDGS Python must be an absolute path")
		}

		find := func(query string) ([]webSearchResult, error) {
			return runDDGSSearch(ctx, python, query)
		}

		if retryWithoutQuotes {
			return searchWithQuoteRecovery(query, find)
		}

		return find(query)
	}
}

func searchWithQuoteRecovery(query string, find func(string) ([]webSearchResult, error)) ([]webSearchResult, error) {
	results, err := find(query)
	if err != nil {
		return results, err
	}

	quoted := searchQuoteReplacer.Replace(query)
	quotes := strings.Count(quoted, `"`)
	if quotes < 2 || quotes%2 != 0 || len(relevantResults(results, searchMatcher(query))) > 0 {
		return results, nil
	}

	// Yahoo can produce different candidates for a quoted query. Relax only
	// the provider request; searchFromSources still filters by the original.
	plain := strings.TrimSpace(strings.ReplaceAll(quoted, `"`, ""))
	if plain == "" {
		return results, nil
	}

	return find(plain)
}

func runDDGSSearch(ctx context.Context, python, query string) ([]webSearchResult, error) {
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
