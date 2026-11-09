package remote

import (
	"context"
	"fmt"
)

// CheckRuntime verifies the tools used to prepare workspaces and run harness
// scripts. SSH authentication alone does not establish a usable environment.
func CheckRuntime(ctx context.Context, connection *Connection) error {
	const probe = `for required in mkdir chmod python3; do
  if ! command -v "$required" >/dev/null 2>&1; then
    printf 'Required remote command is missing from PATH: %s\n' "$required" >&2
    exit 127
  fi
done
python3 -c 'import base64, hashlib, json, os, shutil, sys, tempfile; assert sys.version_info.major == 3, "Python 3 is required"'`
	if _, err := Run(ctx, connection.Client, probe); err != nil {
		return fmt.Errorf("remote runtime is not ready: %w", err)
	}
	return nil
}
