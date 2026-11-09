// Package vscode serves the VS Code web workbench from Zentrale. It never starts
// a local process; workspace services run in a matching remote agent over SSH.
package vscode

import (
	_ "embed"
	"encoding/json"
)

type Distribution struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type Release struct {
	Version string                  `json:"version"`
	Commit  string                  `json:"commit"`
	Web     Distribution            `json:"web"`
	Servers map[string]Distribution `json:"servers"`
}

// The official web and headless server archives share this exact commit. Keep
// release metadata in source control so runtime updates cannot mix protocols.
//
//go:embed release.json
var releaseJSON string

func CurrentRelease() Release {
	var release Release
	if err := json.Unmarshal([]byte(releaseJSON), &release); err != nil {
		panic(err)
	}
	return release
}

func RemoteManifest() string { return releaseJSON }
