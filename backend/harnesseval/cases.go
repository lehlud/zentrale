// Package harnesseval compares the real harness with isolated, verifiable tasks.
package harnesseval

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"strings"
)

// Keep task programs and independent verifiers as ordinary, readable Python.
//
//go:embed fixtures/tally.py
var tallyFixture string

//go:embed fixtures/README.md
var readmeFixture string

//go:embed fixtures/verify_total.py
var totalVerifier string

//go:embed fixtures/counter.py
var counterFixture string

//go:embed fixtures/verify_counter.py
var counterVerifier string

//go:embed fixtures/compute.py
var computeFixture string

//go:embed fixtures/build.py
var buildFixture string

//go:embed fixtures/verify_triangle.py
var triangleVerifier string

type taskCase struct {
	id           string
	prompt       string
	files        map[string]string
	verify       string
	requiredTool string
}

func builtinCases() []taskCase {
	buildDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(buildFixture)))
	return []taskCase{
		{
			id:     "focused-edit",
			prompt: "Fix total(values) in tally.py so it sums every integer, including duplicates and negative values. Preserve README.md. Run a meaningful check and finish the task.",
			files: map[string]string{
				"tally.py":  tallyFixture,
				"README.md": readmeFixture,
			},
			verify: totalVerifier,
		},
		{
			id:     "interactive-input",
			prompt: "Run counter.py as an interactive process with exec_command interactive=true. Use write_stdin to send 2, then 5, then quit, each on its own line. Verify that the same process reports SUM=7 and writes result.txt. Finish after the process exits.",
			files: map[string]string{
				"counter.py": counterFixture,
			},
			verify:       counterVerifier,
			requiredTool: "write_stdin",
		},
		{
			id:     "large-job-output",
			prompt: "Run build.py. Diagnose its final failure using job_logs with tail=true or the returned tailCursor if the output is abbreviated. Fix compute.py, preserve build.py, and verify the calculation. Avoid repeating the full progress log.",
			files: map[string]string{
				"compute.py": computeFixture,
				"build.py":   buildFixture,
			},
			verify: strings.ReplaceAll(triangleVerifier, "EXPECTED_BUILD_SHA256", buildDigest),
		},
	}
}
