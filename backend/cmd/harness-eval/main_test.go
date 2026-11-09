package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestSnapshotIsCopiedWithoutChangingTheSource(t *testing.T) {
	directory := t.TempDir()
	source, destination := filepath.Join(directory, "snapshot.duckdb"), filepath.Join(directory, "copy.duckdb")
	if err := os.WriteFile(source, []byte("original database fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copySnapshot(source, destination); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(source)
	copied, _ := os.ReadFile(destination)
	sourceInfo, _ := os.Stat(source)
	copyInfo, _ := os.Stat(destination)
	if string(original) != string(copied) || sourceInfo.Mode().Perm() != 0644 || copyInfo.Mode().Perm() != 0600 {
		t.Fatal("snapshot or permissions changed")
	}
	if err := copySnapshot(source, destination); err == nil {
		t.Fatal("existing destination overwritten")
	}
}

func TestActiveWALSnapshotIsRejected(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "snapshot.duckdb")
	_ = os.WriteFile(source, []byte("fixture"), 0600)
	_ = os.WriteFile(source+".wal", []byte("active write"), 0600)
	if err := copySnapshot(source, filepath.Join(directory, "copy")); err == nil {
		t.Fatal("active database copy accepted")
	}
}

func TestReportNeverOverwritesItsInputs(t *testing.T) {
	input := filepath.Join(t.TempDir(), "snapshot")
	_ = os.WriteFile(input, []byte("original"), 0600)
	if err := writeReport(input, []byte("report"), input); err == nil {
		t.Fatal("report replaced its input")
	}
	data, _ := os.ReadFile(input)
	if string(data) != "original" {
		t.Fatal("input changed")
	}
}

func TestMixedBillingReportKeepsRealCostsAndMissingPricesVisible(t *testing.T) {
	label := formatCost(domain.UsageSummary{
		RealCost: 0.25, FictionalCost: 0.5, Estimated: true, UnknownPrices: 2,
	})
	for _, expected := range []string{"0.250000 real", "0.500000 equivalent", "estimated", "incomplete: 2 requests without prices"} {
		if !strings.Contains(label, expected) {
			t.Fatalf("report hid %q: %s", expected, label)
		}
	}
}
