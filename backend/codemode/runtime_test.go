package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestToolsFilteringAndInvocationOrder(t *testing.T) {
	calls := []string{}
	result, err := Run(context.Background(), `
const entries = await Promise.all([tools.read({path:"a"}), tools.read({path:"b"})]);
print(entries.map(item => item.name).join(","));
`, []string{"read"}, func(_ string, args map[string]any) (any, error) {
		name := args["path"].(string)
		calls = append(calls, name)

		return map[string]any{"name": name, "large": strings.Repeat("unneeded data", 2000)}, nil
	})
	if err != nil || result.Output != "a,b\n" || result.Calls != 2 || strings.Join(calls, ",") != "a,b" {
		t.Fatal(result, calls, err)
	}
}

func TestLimitsCancellationAndNoHostAccess(t *testing.T) {
	for _, script := range []string{
		`print(typeof process, typeof require, typeof fetch);`,
		`const std = await import("qjs:std"); print(std.loadFile("/etc/passwd"));`,
		`print("x".repeat(20000));`,
		`for(let i=0;i<33;i++) tools.read({});`,
		`let x=[]; for(let i=0;i<100;i++)x.push(new Uint8Array(1024*1024));`,
		`while(true){}`,
	} {
		t.Run(fmt.Sprintf("script-%d", len(script)), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			result, err := Run(ctx, script, []string{
				"read",
			}, func(string, map[string]any) (any, error) { return true, nil })
			if strings.Contains(result.Output, "root:") ||
				len(result.Output) > MaximumOutputBytes ||
				result.Calls > MaximumCalls {
				t.Fatal("host data or unbounded result", result, err)
			}

			switch {
			case strings.Contains(script, "typeof"):
				if err != nil || result.Output != "undefined undefined undefined\n" {
					t.Fatal(result, err)
				}
			case strings.Contains(script, "20000"):
				if err != nil || !result.Truncated {
					t.Fatal(result, err)
				}
			case strings.Contains(script, "loadFile"):
				if err == nil && strings.TrimSpace(result.Output) != "null" {
					t.Fatal(result, err)
				}
			default:
				if err == nil {
					t.Fatal("execution limit was not enforced", result)
				}
			}
		})
	}
}

func TestToolStringArgumentsRoundTrip(t *testing.T) {
	for _, command := range []string{
		"printf record-once >> effects.txt",
		`printf record-once >> '/tmp/TestToolConnectionRecoveryKeepsRecordedFailureWithoutReplayingEf1043936771/001/project '"'"'quotes'"'"' $literal; space/effects.txt'`,
		"printf record-once >> '/tmp/project '\"'\"'quotes'\"'\"' $literal; space/effects.txt'",
		strings.Repeat("long-path/", 30) + "file.txt",
	} {
		encoded, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		source := fmt.Sprintf(`await tools.exec_command({cmd:%s,timeout_ms:5000});`, encoded)
		names := []string{"exec_command"}
		for index := 0; index < 80; index++ {
			names = append(names, fmt.Sprintf("fixture_tool_%d", index))
		}
		result, err := Run(context.Background(), source, names, func(_ string, args map[string]any) (any, error) {
			if args["cmd"] != command {
				t.Errorf("tool string changed: %q, expected %q", args["cmd"], command)
			}
			return true, nil
		})
		if err != nil || result.Calls != 1 {
			t.Fatalf("script=%s result=%+v error=%v", source, result, err)
		}
	}
}

func TestInvalidToolArgumentsNeverReachTheCallback(t *testing.T) {
	for _, arguments := range []string{`[]`, `"text"`, `42`, `{text:"x".repeat(1024*1024)}`} {
		calls := 0
		result, err := Run(context.Background(), "tools.read("+arguments+");", []string{"read"}, func(string, map[string]any) (any, error) {
			calls++
			return nil, nil
		})
		if err == nil || result.Calls != 0 || calls != 0 {
			t.Fatalf("invalid arguments reached callback: %s result=%+v calls=%d error=%v", arguments, result, calls, err)
		}
	}
}
