package agent

import (
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
)

// Public ancestry includes every depth of worker, but never private approvers,
// cycles or project-crossing links. It is read-only and grants no tool authority.
func (engine *Engine) publicThreadRoot(member domain.Agent) (domain.Agent, error) {
	projectID := member.ProjectID
	seen := map[string]bool{}

	for {
		if seen[member.ID] || member.ProjectID != projectID {
			return domain.Agent{}, errors.New("invalid public thread ancestry")
		}

		seen[member.ID] = true

		if member.ParentID == "" {
			if member.Role != "main" {
				return domain.Agent{}, errors.New("public thread must have a main agent")
			}

			return member, nil
		}

		if member.Role != "subagent" {
			return domain.Agent{}, errors.New("private agent is outside the public thread")
		}

		var parent domain.Agent
		if err := engine.Store.Load("agent", member.ParentID, &parent); err != nil {
			return domain.Agent{}, err
		}

		member = parent
	}
}

func (engine *Engine) threadMember(root domain.Agent, agentID string) (domain.Agent, error) {
	if agentID == "" {
		agentID = root.ID
	}

	var member domain.Agent
	if err := engine.Store.Load("agent", agentID, &member); err != nil {
		return member, err
	}

	if member.ProjectID != root.ProjectID || (member.Role != "main" && member.Role != "subagent") {
		return member, errors.New("agent is outside this public thread")
	}

	if member.ID == root.ID {
		return member, nil
	}

	if root.Role != "main" {
		return member, errors.New("workers can read only their own history")
	}

	ancestor, err := engine.publicThreadRoot(member)
	if err != nil {
		return member, err
	}

	if ancestor.ID != root.ID {
		return member, errors.New("agent is outside this public thread")
	}

	return member, nil
}

func (engine *Engine) threadHistory(root domain.Agent, arguments map[string]any) (any, error) {
	if boolArg(arguments, "members") {
		return engine.threadMembersPage(root, arguments)
	}

	member, err := engine.threadMember(root, stringArg(arguments, "agentId"))
	if err != nil {
		return nil, err
	}

	if messageID := stringArg(arguments, "messageId"); messageID != "" {
		return engine.threadMessage(member, messageID, arguments)
	}

	limit, err := outputInteger(arguments, "limit", 10, 1, 50)
	if err != nil {
		return nil, err
	}

	rows, err := engine.Store.MessageExcerpts(member.ID, stringArg(arguments, "before"), stringArg(arguments, "search"), limit+1, 512)
	if err != nil {
		return nil, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	next := ""
	if len(rows) > 0 {
		next, _ = rows[len(rows)-1]["id"].(string)
	}

	return map[string]any{
		"agentId": member.ID, "messages": rows, "hasMore": hasMore, "nextCursor": next,
		"excerpts": true, "readMessage": "Use messageId with field content or calls and UTF-8 byte offset/bytes for exact slices.",
	}, nil
}

func (engine *Engine) threadMembersPage(root domain.Agent, arguments map[string]any) (any, error) {
	current, err := engine.threadMember(root, root.ID)
	if err != nil {
		return nil, err
	}

	offset, err := outputInteger(arguments, "offset", 0, 0, 1000000)
	if err != nil {
		return nil, err
	}

	limit, err := outputInteger(arguments, "limit", 20, 1, 50)
	if err != nil {
		return nil, err
	}

	members := []map[string]any{{"id": current.ID, "name": current.Name, "role": current.Role, "status": current.Status}}
	if current.Role == "main" {
		members, err = engine.threadAgents(current)
		if err != nil {
			return nil, err
		}
	}

	start := min(offset, len(members))
	end := min(start+limit, len(members))

	return map[string]any{"agents": members[start:end], "nextOffset": end, "hasMore": end < len(members)}, nil
}

func (engine *Engine) threadMessage(member domain.Agent, messageID string, arguments map[string]any) (any, error) {
	var message domain.Message
	if err := engine.Store.Load("message", messageID, &message); err != nil {
		return nil, err
	}

	if message.AgentID != member.ID {
		return nil, errors.New("message is outside this thread member")
	}

	content := message.Content
	field := stringArg(arguments, "field")
	switch field {
	case "", "content":
		field = "content"
	case "calls":
		// Public calls are data, not an instruction to invoke them again. Never
		// expose encrypted reasoning or other private provider wire items.
		content = JSON(storedCalls(message))
	default:
		return nil, errors.New("field must be content or calls")
	}

	offset, err := outputInteger(arguments, "offset", 0, 0, len(content))
	if err != nil {
		return nil, err
	}

	limit, err := outputInteger(arguments, "bytes", 4096, 4, 16000)
	if err != nil {
		return nil, err
	}

	for offset < len(content) && !utf8.RuneStart(content[offset]) {
		offset++
	}

	end := min(len(content), offset+limit)
	for end < len(content) && !utf8.RuneStart(content[end]) {
		end--
	}

	return map[string]any{
		"messageId": message.ID, "agentId": member.ID, "role": message.Role, "field": field,
		"content": content[offset:end], "offset": offset, "nextOffset": end,
		"totalBytes": len(content), "hasMore": end < len(content), "unit": "UTF-8 bytes",
	}, nil
}

func (engine *Engine) threadAgents(root domain.Agent) ([]map[string]any, error) {
	children := map[string][]map[string]any{}
	fields := []string{"id", "parentId", "name", "role", "status"}

	for offset := 0; ; offset += 500 {
		rows, err := engine.Store.ListFields("agent", &root.ProjectID, 500, offset, fields)
		if err != nil {
			return nil, err
		}

		for _, row := range rows {
			if stringArg(row, "role") == "subagent" {
				parent := stringArg(row, "parentId")
				children[parent] = append(children[parent], row)
			}
		}

		if len(rows) < 500 {
			break
		}
	}

	result := []map[string]any{{"id": root.ID, "name": root.Name, "role": root.Role, "status": root.Status}}
	seen := map[string]bool{root.ID: true}
	for index := 0; index < len(result); index++ {
		for _, row := range children[stringArg(result[index], "id")] {
			id := stringArg(row, "id")
			if !seen[id] {
				seen[id] = true
				result = append(result, row)
			}
		}
	}

	sort.Slice(result, func(i, j int) bool { return stringArg(result[i], "id") < stringArg(result[j], "id") })
	return result, nil
}
