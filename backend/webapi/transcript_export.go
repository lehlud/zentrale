package webapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/domain"
)

type exportedThread struct {
	ID       string           `json:"id"`
	ParentID string           `json:"parentId"`
	Name     string           `json:"name"`
	Role     string           `json:"role"`
	Messages []map[string]any `json:"messages"`
	Goal     *domain.Goal     `json:"goal,omitempty"`
}

type transcriptArchive struct {
	Version          int                          `json:"version"`
	ExportedAt       time.Time                    `json:"exportedAt"`
	ProjectID        string                       `json:"projectId"`
	ProjectName      string                       `json:"projectName"`
	ProjectDirectory string                       `json:"projectDirectory"`
	Threads          []exportedThread             `json:"threads"`
	Attachments      map[string]map[string][]byte `json:"attachments"`
}

func (server *Server) transcriptExport(request echo.Context) error {
	request.Response().Header().Set("Cache-Control", "no-store")
	if !server.sameOrigin(request.Request()) {
		return echo.ErrForbidden
	}
	user, ok := server.Auth.User(server.token(request.Request()))
	if !ok {
		return echo.ErrUnauthorized
	}

	var root domain.Agent
	if err := server.Store.Load("agent", request.Param("agent"), &root); err != nil {
		return echo.ErrNotFound
	}
	var project domain.Project
	if root.ChatOwnerID != "" {
		if !ownsShortChat(user, root) {
			return echo.ErrNotFound
		}
	} else if err := server.Store.Load("project", root.ProjectID, &project); err != nil || !canSeeProject(user, project) {
		return echo.ErrNotFound
	}

	archive, err := server.archiveTranscript(request.Request().Context(), root, project)
	if err != nil {
		return echo.ErrInternalServerError.WithInternal(err)
	}
	return request.JSON(http.StatusOK, archive)
}

func (server *Server) archiveTranscript(ctx context.Context, root domain.Agent, project domain.Project) (transcriptArchive, error) {
	archive := transcriptArchive{
		Version: 1, ExportedAt: time.Now().UTC(), ProjectID: project.ID,
		ProjectName: project.Name, ProjectDirectory: project.Directory,
		Attachments: make(map[string]map[string][]byte),
	}
	children := make(map[string][]exportedThread)
	if project.ID != "" {
		for offset := 0; ; offset += 500 {
			rows, err := server.Store.ListFields("agent", &project.ID, 500, offset,
				[]string{"id", "parentId", "name", "role", "projectId", "chatOwnerId"})
			if err != nil {
				return archive, err
			}
			for _, row := range rows {
				if row["projectId"] != project.ID || row["chatOwnerId"] != nil && row["chatOwnerId"] != "" {
					continue
				}
				thread, err := convert[exportedThread](row)
				if err != nil {
					return archive, err
				}
				children[thread.ParentID] = append(children[thread.ParentID], thread)
			}
			if len(rows) < 500 {
				break
			}
		}
	}

	// Walk only the selected thread and its descendants, in tree order.
	stack := []exportedThread{{ID: root.ID, Name: root.Name, Role: root.Role}}
	seen := make(map[string]bool)
	for len(stack) > 0 {
		thread := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[thread.ID] {
			continue
		}
		seen[thread.ID] = true

		messages, err := server.Store.MessageHistory(ctx, thread.ID)
		if err != nil {
			return archive, err
		}
		thread.Messages = make([]map[string]any, 0, len(messages))
		for _, message := range messages {
			thread.Messages = append(thread.Messages, mapOf(message))
			for _, attachment := range message.Attachments {
				if err := server.archiveAttachment(&archive, thread.ID, attachment.ID); err != nil {
					return archive, err
				}
			}
		}
		for batch := range slices.Chunk(thread.Messages, 100) {
			if err := server.enrichMessages(batch, true, []string{"path", "kind", "patch", "added", "removed", "truncated", "notice"}); err != nil {
				return archive, err
			}
		}
		if project.ID != "" && thread.Role != "approver" {
			thread.Goal, err = server.Engine.CurrentGoal(thread.ID)
			if err != nil {
				return archive, err
			}
		}

		archive.Threads = append(archive.Threads, thread)
		for i := len(children[thread.ID]) - 1; i >= 0; i-- {
			stack = append(stack, children[thread.ID][i])
		}
	}
	return archive, nil
}

func (server *Server) archiveAttachment(archive *transcriptArchive, agentID, id string) error {
	files := archive.Attachments[agentID]
	if _, ok := files[id]; ok {
		return nil
	}
	_, data, err := server.Store.Attachment(agentID, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if files == nil {
		files = make(map[string][]byte)
		archive.Attachments[agentID] = files
	}
	// Older messages may reference an attachment that only lived on a VM.
	// Keep its metadata, but don't start a VM or read arbitrary workspace files.
	files[id] = data
	return nil
}
