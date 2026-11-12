package agent

import (
	"context"
	"fmt"

	"github.com/lehlud/zentrale/remote"
)

func prepareRemoteWorkspace(
	ctx context.Context,
	connection *remote.Connection,
	directory, temporaryDirectory string,
) error {
	// All scratch roots are direct children of /tmp. Do not chmod through a
	// malicious scratch-root symlink before the sandboxed file operation starts.
	script := "import os,sys\np=sys.argv[1]\ntry: os.mkdir(p,0o700)\nexcept FileExistsError: pass\nf=os.open(p,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)\nos.fchmod(f,0o700)\nos.close(f)"
	command := "mkdir -p -- " + remote.Quote(directory) + " && python3 -I -c " + remote.Quote(script) + " " + remote.Quote(temporaryDirectory)
	if _, err := remote.Run(ctx, connection.Client, command); err != nil {
		return fmt.Errorf("prepare remote workspace: %w", err)
	}

	return nil
}
