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
	command := "mkdir -p -- " + remote.Quote(directory) + " && " + remote.PrepareProjectScratchCommand(temporaryDirectory)
	if _, err := remote.Run(ctx, connection, command); err != nil {
		return fmt.Errorf("prepare remote workspace: %w", err)
	}

	return nil
}
