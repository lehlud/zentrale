// A disposable SSH peer for browser tests. Closing stdin shuts it down.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/lehlud/zentrale/internal/testssh"
)

func main() {
	server, err := testssh.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer server.Close()

	if err := json.NewEncoder(os.Stdout).Encode(server); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	_, _ = io.Copy(io.Discard, os.Stdin)
}
