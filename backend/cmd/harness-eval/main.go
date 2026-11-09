package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/harnesseval"
	"github.com/lehlud/zentrale/providers"
)

func copySnapshot(source, destination string) error {
	if _, err := os.Stat(source + ".wal"); err == nil {
		return errors.New("use a closed database snapshot without a WAL file")
	}

	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	before, err := input.Stat()
	if err != nil {
		return err
	}

	if !before.Mode().IsRegular() {
		return errors.New("snapshot must be a regular database file")
	}

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	_, copyError := io.Copy(output, input)
	closeError := output.Close()

	if copyError != nil {
		return copyError
	}

	if closeError != nil {
		return closeError
	}

	after, err := input.Stat()
	if err != nil {
		return err
	}

	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return errors.New("snapshot changed while copying")
	}

	return nil
}

func writeReport(destination string, data []byte, inputs ...string) error {
	target, err := filepath.Abs(destination)
	if err != nil {
		return err
	}

	targetInfo, targetError := os.Lstat(target)
	if targetError == nil && targetInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("report destination must not be a symlink")
	}

	for _, input := range inputs {
		source, err := filepath.Abs(input)
		if err != nil {
			return err
		}

		sourceInfo, _ := os.Stat(source)
		if source == target ||
			targetError == nil &&
				sourceInfo != nil &&
				os.SameFile(sourceInfo, targetInfo) {
			return errors.New("report destination must differ from the snapshot and plan")
		}
	}

	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	if err := file.Chmod(0600); err != nil {
		return err
	}

	_, err = file.Write(data)

	return err
}

func run() error {
	syscall.Umask(0077)
	snapshot := flag.String("snapshot", "", "closed DuckDB snapshot containing providers, models and SSH configuration")
	planPath := flag.String("plan", "", "evaluation plan JSON; model requests use the configured provider accounts")
	reportPath := flag.String(
		"report",
		"harness-eval-report.json",
		"private result report, without credentials or conversation contents",
	)

	flag.Parse()

	if *snapshot == "" || *planPath == "" {
		flag.Usage()
		return errors.New("snapshot and plan are required")
	}

	file, err := os.Open(*planPath)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, 1024*1024))
	decoder.DisallowUnknownFields()
	var plan harnesseval.Plan
	if err := decoder.Decode(&plan); err != nil {
		return err
	}

	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("plan must contain one JSON object")
	}

	directory, err := os.MkdirTemp("", "zentrale-harness-eval-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)

	copyPath := filepath.Join(directory, "evaluation.duckdb")
	if err := copySnapshot(*snapshot, copyPath); err != nil {
		return err
	}

	store, err := datastore.Open(copyPath)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	report, runError := harnesseval.Run(ctx, store, providers.New(), plan)
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}

	if err := writeReport(*reportPath, append(encoded, '\n'), *snapshot, *planPath); err != nil {
		return err
	}

	table := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "Model\tCase\tPassed\tRequests\tTokens (all agents)\tUSD\tMilliseconds")
	failed := 0

	for _, result := range report.Runs {
		tokens := result.TotalUsage.InputTokens + result.TotalUsage.OutputTokens
		tokenText := fmt.Sprint(tokens)

		if !result.TokensReported {
			tokenText += " (estimated)"
		}

		cost := formatCost(result.TotalUsage)
		fmt.Fprintf(
			table,
			"%s/%s\t%s\t%t\t%d\t%s\t%s\t%d\n",
			result.Model.ProviderID,
			result.Model.ModelID,
			result.Case,
			result.Passed,
			result.ModelRequests,
			tokenText,
			cost,
			result.DurationMillis,
		)

		if !result.Passed || result.CleanupError != "" {
			failed++
		}
	}

	_ = table.Flush()
	fmt.Println("Report:", *reportPath)

	if runError == nil && failed > 0 {
		return fmt.Errorf("%d evaluation runs failed", failed)
	}

	return runError
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
