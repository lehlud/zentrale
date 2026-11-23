package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lehlud/zentrale/domain"
)

// Earlier verdicts are optional evidence, not permission. Large context windows
// must not turn each short approval into a replay of tens of thousands of old
// review tokens. Current instructions, the request and inspections are separate.
const maximumReviewHistoryTokens = 8192

// Only recent, complete reviews are candidates for the optional history.
// Fetch them at the database boundary before decoding snapshots and outputs.
const reviewHistoryGroups = 16

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

	// Partition completed reviews into fixed bounded sections. A sliding tail
	// would evict its first group on almost every new review, rewriting the
	// cached prefix. Only roll over when another complete group fills a section.
	budget := min(maximumReviewHistoryTokens, contextWindow/4)
	section, used := 0, 0
	for group := 0; group < len(starts)-1; group++ {
		cost := estimatedMessageTokens(messages[starts[group]:starts[group+1]])
		if cost > budget {
			section, used = group+1, 0
			continue
		}

		if used+cost > budget {
			section, used = group, 0
		}

		used += cost
	}

	start := starts[section]
	if used > contextWindow*70/100-required {
		// An unusually large current review has priority over optional history.
		// Keep it whole instead of reshuffling older groups to fill spare space.
		start = latest
	}

	result := append([]domain.Message(nil), preamble...)

	if start > starts[0] {
		result = append(result, domain.Message{
			Role: "context",
			Content: "Harness review budget: older review groups were omitted as whole records. " +
				"The leading approval snapshot contains the complete current user instructions. " +
				"Missing past decisions never imply permission; inspect history if needed.",
		})
	}

	result = append(result, messages[start:]...)

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
