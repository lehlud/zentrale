// Package codemode evaluates tool orchestration in a memory-limited WASM VM.
// Project execution remains behind the supplied, reviewed tool callback.
package codemode

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fastschema/qjs"
)

const MaximumSourceBytes = 64 * 1024
const MaximumCalls = 32
const MaximumOutputBytes = 16000

var runtimeSlots = make(chan struct{}, 4)

var ErrInterrupted = errors.New("tool batch interrupted by new user instructions; read the updated task before continuing")

// qjs v0.0.6, with a 64 MiB WebAssembly memory maximum. prepare_wasm.py
// reproduces this change from the pinned upstream checksum. Licenses are kept
// alongside the module. The hard VM ceiling also covers typed-array buffers.
//
//go:embed qjs.wasm
var sandboxModule []byte

type Call func(name string, arguments map[string]any) (any, error)

type Result struct {
	Output    string `json:"output"`
	Calls     int    `json:"calls"`
	Truncated bool   `json:"truncated"`
}

// Calls run in invocation order, including when wrapped in Promise.all. This
// preserves the agent's single independent reviewer and mutation ordering.
func Run(ctx context.Context, source string, names []string, call Call) (result Result, err error) {
	if source == "" || len(source) > MaximumSourceBytes {
		return result, errors.New("code_mode source must contain 1–65536 UTF-8 bytes")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var interrupted error
	select {
	case runtimeSlots <- struct{}{}:
		defer func() { <-runtimeSlots }()
	case <-ctx.Done():
		return result, ctx.Err()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			message := strings.SplitN(fmt.Sprint(recovered), "\n", 2)[0]
			err = fmt.Errorf("code_mode runtime stopped: %.500s", message)
		}
		if interrupted != nil {
			err = interrupted
		} else if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	vm, err := qjs.New(qjs.Option{
		Context: ctx, CloseOnContextDone: true,
		// A non-directory mount provides no usable filesystem capability. WASI
		// confines paths to this mount, including module loaders and ../ paths.
		CWD: os.DevNull, Stdout: io.Discard, Stderr: io.Discard,
		MemoryLimit: 32 * 1024 * 1024, MaxStackSize: 512 * 1024,
		MaxExecutionTime: 120000,
		QuickJSWasmBytes: sandboxModule,
	})
	if err != nil {
		return result, err
	}
	defer vm.Close()
	js := vm.Context()
	completed := false
	var scriptError string
	js.SetFunc("__finish", func(this *qjs.This) (*qjs.Value, error) {
		completed = true
		return js.NewUndefined(), nil
	})
	js.SetFunc("__fail", func(this *qjs.This) (*qjs.Value, error) {
		if len(this.Args()) > 0 {
			scriptError = fmt.Sprintf("%.500s", this.Args()[0].String())
		}
		return js.NewUndefined(), nil
	})
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	js.SetFunc("__call", func(this *qjs.This) (*qjs.Value, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		args := this.Args()
		if len(args) != 2 || !allowed[args[0].String()] {
			return nil, errors.New("unknown code_mode tool")
		}
		if result.Calls >= MaximumCalls {
			return nil, errors.New("code_mode call limit reached; continue in another model round")
		}
		encoded, err := args[1].JSONStringify()
		if err != nil || len(encoded) > 1024*1024 {
			return nil, errors.New("tool arguments must be a JSON object of at most 1 MiB")
		}
		var arguments map[string]any
		if json.Unmarshal([]byte(encoded), &arguments) != nil || arguments == nil {
			return nil, errors.New("tool arguments must be a JSON object")
		}
		result.Calls++
		value, failure := call(args[0].String(), arguments)
		if errors.Is(failure, ErrInterrupted) {
			interrupted = failure
			cancel()
			return nil, failure
		}
		if failure != nil {
			value = map[string]any{"error": failure.Error()}
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return js.ParseJSON(string(data)), nil
	})
	js.SetFunc("__print", func(this *qjs.This) (*qjs.Value, error) {
		for index, value := range this.Args() {
			text := value.String()
			if value.IsObject() {
				if encoded, failure := value.JSONStringify(); failure == nil {
					text = encoded
				}
			}
			if index > 0 {
				appendOutput(&result, " ")
			}
			appendOutput(&result, text)
		}
		appendOutput(&result, "\n")
		return js.NewUndefined(), nil
	})
	encodedNames, _ := json.Marshal(names)
	bootstrap := `const tools = Object.freeze(Object.fromEntries(` + string(encodedNames) + `
  .map(name => [name, args => __call(name, args ?? {})])));
const print = (...values) => __print(...values);
const text = print;
const console = Object.freeze({log: print, info: print, error: print});
`
	value, err := js.Eval("code-mode.js", qjs.Code(bootstrap+"\n(async () => {\n"+source+"\n})().then(__finish, error => __fail(String(error)))"))
	if err != nil {
		return result, err
	}
	defer value.Free()
	if scriptError != "" {
		return result, errors.New(scriptError)
	}
	if !completed {
		return result, errors.New("code_mode did not complete; a runtime resource limit may have been reached; inspect state before retrying")
	}
	return result, nil
}

func appendOutput(result *Result, value string) {
	remaining := MaximumOutputBytes - len(result.Output)
	if len(value) > remaining {
		end := remaining
		for end > 0 && end < len(value) && !utf8.RuneStart(value[end]) {
			end--
		}
		value = value[:end]
		result.Truncated = true
	}
	result.Output += strings.ToValidUTF8(value, "�")
}
