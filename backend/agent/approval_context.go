package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lehlud/zentrale/domain"
)

// The current review and its ongoing inspections are mandatory. Earlier reviews
// are optional evidence, admitted as complete groups under a separate budget.
// Every newest request still carries all current original user instructions.
func budgetReviewHistory(messages []domain.Message, contextWindow, overhead int) ([]domain.Message, error) {
	messages = compactReviewSnapshots(messages)
	starts := []int{}
	for index, message := range messages {
		if message.Role == "event" {
			starts = append(starts, index)
		}
	}

	if len(starts) == 0 {
		return messages, nil
	}

	latest := starts[len(starts)-1]
	preamble := messages[:starts[0]]
	required := estimatedMessageTokens(preamble) + estimatedMessageTokens(messages[latest:]) + overhead
	if required > contextWindow*70/100 {
		return nil, errors.New("current approval request and inspections exceed the review context budget; use smaller tool arguments or a larger approver model")
	}

	budget := min(contextWindow/4, contextWindow*70/100-required)
	selected := map[int]bool{latest: true}

	for group := len(starts) - 2; group >= 0; group-- {
		cost := estimatedMessageTokens(messages[starts[group]:starts[group+1]])
		if cost <= budget {
			selected[starts[group]] = true
			budget -= cost
		}
	}

	result := append([]domain.Message(nil), preamble...)

	if len(selected) < len(starts) {
		result = append(result, domain.Message{
			Role: "context",
			Content: "Harness review budget: older review groups were omitted as whole records. " +
				"The leading approval snapshot contains the complete current user instructions. " +
				"Missing past decisions never imply permission; inspect history if needed.",
		})
	}

	for group, start := range starts {
		if !selected[start] {
			continue
		}

		end := len(messages)

		if group+1 < len(starts) {
			end = starts[group+1]
		}

		result = append(result, messages[start:end]...)
	}

	return result, nil
}

// Keep one complete current snapshot before the review history. Normalize every
// review identically, including the newest: appending a review must not rewrite
// the previous newest message and invalidate the cached conversation prefix.
// Snapshot references identify historical versions; original audit data remains
// available to inspection. A real instruction/project change changes the prefix.
func compactReviewSnapshots(messages []domain.Message) []domain.Message {
	result := append([]domain.Message(nil), messages...)
	current := ""

	for index, message := range messages {
		if message.Role != "event" {
			continue
		}

		var review map[string]any
		if json.Unmarshal([]byte(message.Content), &review) != nil ||
			review["mainAgentId"] == nil ||
			review["action"] == nil {
			continue
		}

		snapshot := JSON(map[string]any{
			"userInstructions": review["userInstructions"],
			"project":          review["project"],
			"sources":          review["sources"],
		})

		delete(review, "userInstructions")
		delete(review, "project")
		delete(review, "sources")
		review["contextSnapshotRef"] = fmt.Sprintf("%x", sha256.Sum256([]byte(snapshot)))
		result[index].Content = JSON(review)
		current = snapshot
	}

	if current != "" {
		result = append([]domain.Message{
			{
				Role:    "context",
				Content: "Current approval snapshot (complete original user instructions, project and sources):\n" + current,
			},
		}, result...)
	}

	return result
}
