package agent

import (
	"context"
	"errors"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

// Always resolve from persisted family ownership, never a child-supplied
// approver reference. This also fails closed for unmigrated/corrupt children.
func (engine *Engine) familyApprover(record domain.Agent) (domain.Agent, error) {
	root, err := engine.approvalRoot(record)
	if err != nil {
		return domain.Agent{}, err
	}
	var approver domain.Agent
	if err := engine.Store.Load("agent", root.ApproverID, &approver); err != nil {
		return domain.Agent{}, err
	}
	if approver.Role != "approver" || approver.ParentID != root.ID || approver.ProjectID != root.ProjectID {
		return domain.Agent{}, errors.New("invalid shared family approver")
	}
	return approver, nil
}

// One review owns the shared context through append/inspection/decision/status
// transitions. Cancellation while queued never appends evidence or executes.
func (engine *Engine) acquireReview(ctx context.Context, approverID string) (func(), error) {
	value, _ := engine.reviewGates.LoadOrStore(approverID, make(chan struct{}, 1))
	gate := value.(chan struct{})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return nil, err
		}
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Before workers resume, rebind legacy children to the existing root reviewer.
// Old private review histories remain intact and read-only for auditability;
// they are archived, never replayed, merged into authority, or silently deleted.
func (engine *Engine) migrateSharedApprovers() error {
	rows, err := engine.listAll("agent", nil)
	if err != nil {
		return err
	}
	writes := []datastore.ResourceWrite{}
	for _, row := range rows {
		if stringArg(row, "role") != "subagent" {
			continue
		}
		var child domain.Agent
		if err := decode(JSON(row), &child); err != nil {
			return err
		}
		shared, err := engine.familyApprover(child)
		if err != nil {
			return err
		}
		if child.ApproverID == shared.ID {
			continue
		}
		var old domain.Agent
		if engine.Store.Load("agent", child.ApproverID, &old) == nil && old.Role == "approver" && old.ParentID == child.ID && old.ProjectID == child.ProjectID {
			old.Status, old.Error = "archived", ""
			writes = append(writes, datastore.ResourceWrite{Kind: "agent", ID: old.ID, Parent: old.ProjectID, Value: old})
		}
		child.ApproverID = shared.ID
		writes = append(writes, datastore.ResourceWrite{Kind: "agent", ID: child.ID, Parent: child.ProjectID, Value: child})
	}
	if len(writes) == 0 {
		return nil
	}
	return engine.Store.SaveResources(writes)
}
