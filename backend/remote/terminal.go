package remote

import (
	_ "embed"
	"encoding/base64"
)

//go:embed scripts/terminal.py
var terminalRelay string

func terminalCommand(command string) string {
	return "python3 -I -u -c " + Quote(terminalRelay) + " " + Quote(base64.StdEncoding.EncodeToString([]byte(command)))
}
