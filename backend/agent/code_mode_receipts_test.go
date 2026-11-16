package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/providers"
)

func TestCodeModeFailureReportsCompletedMutationsAndJobsWithoutReplayingThem(t *testing.T) {
	var providerCalls atomic.Int32
	engine, record, project := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		providerCalls.Add(1)
		return providers.Result{Text: `{"allowed":true,"reason":"","blanket":false}`, UsageKnown: true}, nil
	}), shellPeerHandler("/bin/sh", os.Environ()))
	code := `
const edit = await tools.apply_patch({input:"*** Begin Patch\n*** Add File: changed.txt\n+done\n*** End Patch"});
if(edit.error) throw new Error(edit.error);
const launch = await tools.exec_command({cmd:"printf once >> executions.txt",timeout_ms:30000,background:true});
if(launch.error) throw new Error(launch.error);
throw new Error("fixture failed after the tools returned");
`
	if err := engine.executeCalls(context.Background(), record, []providers.Call{{
		ID: "receipt-test", Name: "code_mode", Arguments: map[string]any{"code": code},
	}}); err != nil {
		t.Fatal(err)
	}

	history, err := engine.loadHistory(record.ID)
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	var outcome struct {
		Error  string `json:"error"`
		Result struct {
			MessageID string            `json:"messageId"`
			Execution string            `json:"execution"`
			Receipts  []codeModeReceipt `json:"receipts"`
			Recovery  string            `json:"recovery"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(history[0].Content), &outcome); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outcome.Error, "fixture failed") || outcome.Result.Execution != "tool_calls_attempted" ||
		outcome.Result.MessageID != history[0].ID ||
		len(outcome.Result.Receipts) != 2 || outcome.Result.Receipts[0].Tool != "apply_patch" ||
		outcome.Result.Receipts[0].Status != "ok" || outcome.Result.Receipts[0].ChangedFiles != 1 ||
		outcome.Result.Receipts[1].Tool != "exec_command" || outcome.Result.Receipts[1].Status != "ok" ||
		outcome.Result.Receipts[1].JobID == "" || !strings.Contains(outcome.Result.Recovery, "background job") {
		t.Fatal("failed batch lost completed tool receipts or confused job launch with completion", outcome)
	}

	beforeReadback := providerCalls.Load()
	for index, receipt := range outcome.Result.Receipts {
		if receipt.Activity != index+1 {
			t.Fatal("receipt does not identify its recorded inner result", receipt)
		}
		value, err := engine.dispatch(context.Background(), record, codeModeCall("tool_output", map[string]any{
			"messageId": outcome.Result.MessageID, "activity": receipt.Activity,
		}))
		if err != nil {
			t.Fatal("could not read the saved result after JavaScript failed", err)
		}
		result := value.(map[string]any)
		if result["recordingTruncated"] != false ||
			(index == 0 && !strings.Contains(result["content"].(string), "changed.txt")) ||
			(index == 1 && !strings.Contains(result["content"].(string), receipt.JobID)) {
			t.Fatal("readback lost the confirmed file or launched job", result)
		}
	}
	if providerCalls.Load() != beforeReadback {
		t.Fatal("reading a saved result invoked the model again")
	}

	content, err := os.ReadFile(filepath.Join(project.Directory, "changed.txt"))
	if err != nil || string(content) != "done\n" {
		t.Fatal("completed patch was lost", string(content), err)
	}
	jobs, err := engine.listAll("job", &record.ID)
	if err != nil || len(jobs) != 1 || jobs[0]["id"] != outcome.Result.Receipts[1].JobID {
		t.Fatal("failed JavaScript replayed or lost its launched command", jobs, err)
	}
}

func TestCodeModeSyntaxFailureReportsThatNoToolStarted(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("JavaScript syntax failure must not spend an approval call")
		return providers.Result{}, nil
	}))
	result, err := engine.runCodeMode(context.Background(), record, map[string]any{"code": `const edits = [`})
	if err == nil {
		t.Fatal("invalid JavaScript succeeded")
	}
	response := result.(map[string]any)
	if response["execution"] != "no_tool_calls_started" || len(response["receipts"].([]codeModeReceipt)) != 0 ||
		!strings.Contains(response["recovery"].(string), "No tool calls started") {
		t.Fatal("syntax failure left mutation status ambiguous", result, err)
	}
}

func TestCodeModeIgnoredToolErrorsStillReturnPerCallReceipts(t *testing.T) {
	engine, record := testEngine(t, nil)
	result, err := engine.runCodeMode(context.Background(), record, map[string]any{
		"code": `await tools.apply_patch({input:"invalid patch"}); print("script continued");`,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := result.(map[string]any)
	receipts, _ := response["receipts"].([]codeModeReceipt)
	if response["execution"] == "no_tool_calls_started" || len(receipts) != 1 || receipts[0].Status != "error" {
		t.Fatal("an ignored tool error was presented as an empty or successful batch", result)
	}
}
