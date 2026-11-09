package agent

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestFirstInstructionNamesConversationAndKeepsLaterTitle(t *testing.T) {
	engine, agent := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{Text: "Completed", UsageKnown: true}, nil
	}))

	agent.Name = "Neue Unterhaltung"

	if err := engine.saveAgent(agent); err != nil {
		t.Fatal(err)
	}

	if err := engine.Prompt(agent.ID, "  Prüfe das Projekt\n und seine Tests.  "); err != nil {
		t.Fatal(err)
	}

	finished := waitForAgent(t, engine, agent.ID)
	if finished.Name != "Prüfe das Projekt und seine Tests." {
		t.Fatalf("first instruction did not name the conversation: %q", finished.Name)
	}

	// Neither later instructions nor a new title supplied by the user may be
	// replaced automatically when a conversation resumes.
	if err := engine.nameFirstConversation(&finished, "Another task"); err != nil {
		t.Fatal(err)
	}

	if finished.Name != "Prüfe das Projekt und seine Tests." {
		t.Fatalf("later instruction replaced the title: %q", finished.Name)
	}

	finished.Name = "New conversation"

	if err := engine.nameFirstConversation(&finished, "Another task"); err != nil {
		t.Fatal(err)
	}

	if finished.Name != "New conversation" {
		t.Fatal("a conversation with existing history was renamed automatically")
	}
}

func TestConversationRenameValidationAndUnicodeTitle(t *testing.T) {
	engine, agent := testEngine(t, nil)
	if err := engine.RenameAgent(agent.ID, "  Überblick über Änderungen  "); err != nil {
		t.Fatal(err)
	}

	var saved domain.Agent
	if err := engine.Store.Load("agent", agent.ID, &saved); err != nil {
		t.Fatal(err)
	}

	if saved.Name != "Überblick über Änderungen" {
		t.Fatalf("rename not persisted: %q", saved.Name)
	}

	for _, invalid := range []string{" ", "two\nlines", "null\x00byte", strings.Repeat("ä", 201)} {
		if err := engine.RenameAgent(agent.ID, invalid); err == nil {
			t.Fatalf("accepted invalid conversation name: %q", invalid)
		}
	}

	engine.mu.Lock()
	engine.running[agent.ID] = func() {}
	engine.mu.Unlock()

	if err := engine.RenameAgent(agent.ID, "Busy"); err == nil {
		t.Fatal("rename could overwrite a running agent's title")
	}

	engine.mu.Lock()
	delete(engine.running, agent.ID)
	engine.mu.Unlock()
	agent.Name = "New conversation"

	if err := engine.nameFirstConversation(&agent, strings.Repeat("ä", 100)); err != nil {
		t.Fatal(err)
	}

	if utf8.RuneCountInString(agent.Name) != 70 ||
		!strings.HasSuffix(agent.Name, "…") ||
		!utf8.ValidString(agent.Name) {
		t.Fatalf("long Unicode title not shortened safely: %q", agent.Name)
	}
}
