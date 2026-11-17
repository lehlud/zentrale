//go:build !native_test_ssh

package main

import "github.com/lehlud/zentrale/agent"

func configureSyntheticSSHFixtures(_ *agent.Engine) {}
