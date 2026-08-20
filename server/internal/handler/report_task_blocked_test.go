package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ReportTaskBlocked (POST /api/tasks/blocked) is `multica task blocked
// --reason`'s server side: a running agent self-reporting a genuine
// environment/permission blocker so the run lands as failed/agent_blocked
// instead of the daemon reporting it as completed (VUH-140).

func reportTaskBlockedRequest(agentID, taskID, reason string) *http.Request {
	r := newRequest("POST", "/api/tasks/blocked", map[string]any{"reason": reason})
	r.Header.Set("X-Agent-ID", agentID)
	r.Header.Set("X-Task-ID", taskID)
	return r
}

func TestReportTaskBlocked_RunningTask_MarksFailedAgentBlocked(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "TaskBlockedAgent", []byte("[]"))
	issue := createIssueForTest(t, map[string]any{"title": "task blocked fixture", "status": "in_progress"})
	taskID := createHandlerTestTaskForAgentOnIssue(t, agentID, issue.ID)

	w := httptest.NewRecorder()
	testHandler.ReportTaskBlocked(w, reportTaskBlockedRequest(agentID, taskID, "checkout path is not findable"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status, failureReason, errMsg string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, COALESCE(failure_reason, ''), COALESCE(error, '') FROM agent_task_queue WHERE id = $1
	`, taskID).Scan(&status, &failureReason, &errMsg); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if status != "failed" {
		t.Fatalf("task status = %q, want failed", status)
	}
	if failureReason != "agent_blocked" {
		t.Fatalf("failure_reason = %q, want agent_blocked", failureReason)
	}
	if errMsg != "checkout path is not findable" {
		t.Fatalf("error = %q, want the reported reason", errMsg)
	}
}

func TestReportTaskBlocked_WrongAgent_Returns403AndLeavesTaskRunning(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "TaskBlockedOwnerAgent", []byte("[]"))
	victimIssue := createIssueForTest(t, map[string]any{"title": "task blocked victim", "status": "in_progress"})
	taskID := createHandlerTestTaskForAgentOnIssue(t, agentID, victimIssue.ID)
	otherAgentID := createHandlerTestAgent(t, "TaskBlockedImpersonatorAgent", []byte("[]"))
	callerIssue := createIssueForTest(t, map[string]any{"title": "task blocked caller", "status": "in_progress"})
	otherTaskID := createHandlerTestTaskForAgentOnIssue(t, otherAgentID, callerIssue.ID)

	w := httptest.NewRecorder()
	// otherAgentID reports blocked, but names agentID's task — X-Task-ID must
	// belong to the calling agent, not just any running task.
	testHandler.ReportTaskBlocked(w, reportTaskBlockedRequest(otherAgentID, taskID, "not my task"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "running" {
		t.Fatalf("victim task was mutated: status = %q", got)
	}
	if got := taskStatus(t, otherTaskID); got != "running" {
		t.Fatalf("caller's own task was mutated: status = %q", got)
	}
}

func TestReportTaskBlocked_NonRunningTask_DoesNotSucceed(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "TaskBlockedCompletedAgent", []byte("[]"))
	issue := createIssueForTest(t, map[string]any{"title": "task blocked completed fixture", "status": "in_progress"})
	taskID := createHandlerTestTaskForAgentOnIssue(t, agentID, issue.ID)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1
	`, taskID); err != nil {
		t.Fatalf("mark task completed: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.ReportTaskBlocked(w, reportTaskBlockedRequest(agentID, taskID, "too late"))
	// failTask is idempotent for terminal tasks: it answers 200 with the task
	// untouched rather than rewriting a settled outcome. What matters here is
	// that a late report cannot turn a completed run into a failed one.
	if got := taskStatus(t, taskID); got != "completed" {
		t.Fatalf("completed task status changed to %q", got)
	}
}

func TestReportTaskBlocked_MissingReason_Returns400(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "TaskBlockedNoReasonAgent", []byte("[]"))
	taskID := createHandlerTestTaskForAgent(t, agentID)

	w := httptest.NewRecorder()
	testHandler.ReportTaskBlocked(w, reportTaskBlockedRequest(agentID, taskID, ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "running" {
		t.Fatalf("task was mutated on a rejected request: status = %q", got)
	}
}
