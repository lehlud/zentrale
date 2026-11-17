//go:build native_test_ssh

package main

import "github.com/lehlud/zentrale/agent"

// Only the browser fixture build uses protocol-simulating SSH servers. Normal
// binaries always use the reconnectable daemon; errors never select this path.
func configureSyntheticSSHFixtures(engine *agent.Engine) {
	engine.Remote.Connector.NativeSessionsForTests = true
}
