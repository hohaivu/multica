package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedChatTask inserts a running chat/direct task (no issue_id) for agentID,
// with originator_user_id set, and returns its id — the exact shape of a `pm`
// run driven from a chat session (VUH-96).
func seedChatTask(t *testing.T, agentID, originatorID string) string {
	t.Helper()
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, originator_user_id, accountable_user_id, started_at)
		VALUES ($1, $2, 'running', 0, $3, $3, now())
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), originatorID).Scan(&taskID); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})
	return taskID
}

// seedTaskOnIssueWithOriginator inserts a running task bound to issueID with a
// given originator_user_id, for the cross-issue negative test.
func seedTaskOnIssueWithOriginator(t *testing.T, agentID, issueID, originatorID string) string {
	t.Helper()
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, originator_user_id, accountable_user_id, started_at)
		VALUES ($1, $2, 'running', 0, $3, $4, $4, now())
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), issueID, originatorID).Scan(&taskID); err != nil {
		t.Fatalf("seed task on issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})
	return taskID
}

// createPrivateAgentForOwner inserts a private agent under an owner obtained
// from an existing privateAgentTestFixture call, for tests that need a SECOND
// private agent under the same owner.
func createPrivateAgentForOwner(t *testing.T, name, ownerID string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb,
		        $3, 'private', 1, $4, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id
	`, testWorkspaceID, name, handlerTestRuntimeID(t), ownerID).Scan(&agentID); err != nil {
		t.Fatalf("create private agent for owner: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID
}

// TestCreateComment_ChatDrivenMentionCarriesOriginatorAcrossHop is the VUH-96
// replay: an agent comment authored from a chat task (issue_id NULL) must
// stamp source_task_id so the run it wakes inherits the chat's human
// originator — and that woken run's OWN mention of another of its owner's
// private agents must then be queued, not blocked/invocation_not_allowed.
func TestCreateComment_ChatDrivenMentionCarriesOriginatorAcrossHop(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	pmAgentID := createHandlerTestAgent(t, "VUH96 PM", nil)
	leaderID, ownerID, _ := privateAgentTestFixture(t)
	nextAgentID := createPrivateAgentForOwner(t, "VUH96 Next Hop Agent", ownerID)
	chatTaskID := seedChatTask(t, pmAgentID, ownerID)
	issueID := createCommentTriggerPreviewIssue(t, "chat-driven mention", "", "")

	// Hop 1: pm, driven from chat, mentions the owner's private leader.
	content := fmt.Sprintf("[@Leader](mention://agent/%s) please pick this up", leaderID)
	r := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", map[string]any{"content": content}), "id", issueID)
	r.Header.Set("X-Agent-ID", pmAgentID)
	r.Header.Set("X-Task-ID", chatTaskID)
	w := httptest.NewRecorder()
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment (hop 1): expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp1 CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp1); err != nil {
		t.Fatalf("decode comment (hop 1): %v", err)
	}
	if o := findCommentOutcome(t, resp1.TriggerOutcomes, leaderID); o.Status != DispatchQueued {
		t.Fatalf("hop 1 outcome = %+v, want queued (create-time gate reads the chat task's originator directly)", o)
	}

	// The fix: the comment must carry the chat task's lineage forward.
	var sourceTaskID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT source_task_id FROM comment WHERE id = $1`, resp1.ID).Scan(&sourceTaskID); err != nil {
		t.Fatalf("read comment source_task_id: %v", err)
	}
	if !sourceTaskID.Valid || uuidToString(sourceTaskID) != chatTaskID {
		t.Fatalf("comment.source_task_id = %v, want the chat task %s (a task bound to no issue has no cross-issue authority to protect, so its lineage must carry, MUL-4857)", sourceTaskID, chatTaskID)
	}

	// And the leader's newly enqueued task must resolve originator = ownerID
	// through that lineage, not sit unattributed.
	var leaderTaskID string
	var leaderOriginator pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT id, originator_user_id FROM agent_task_queue
		WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'
	`, issueID, leaderID).Scan(&leaderTaskID, &leaderOriginator); err != nil {
		t.Fatalf("read leader task: %v", err)
	}
	if !leaderOriginator.Valid || uuidToString(leaderOriginator) != ownerID {
		t.Fatalf("leader task originator_user_id = %v, want owner %s (unattributed = the VUH-96 bug)", leaderOriginator, ownerID)
	}

	// Hop 2: the leader's own run, correctly attributed now, mentions another
	// of its owner's private agents — the exact symptom tech-lead hit. Must be
	// queued, not blocked/invocation_not_allowed.
	content2 := fmt.Sprintf("[@Next](mention://agent/%s) resume this", nextAgentID)
	r2 := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", map[string]any{"content": content2, "parent_id": resp1.ID}), "id", issueID)
	r2.Header.Set("X-Agent-ID", leaderID)
	r2.Header.Set("X-Task-ID", leaderTaskID)
	w2 := httptest.NewRecorder()
	testHandler.CreateComment(w2, r2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("CreateComment (hop 2): expected 201, got %d: %s", w2.Code, w2.Body.String())
	}
	var resp2 CommentResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode comment (hop 2): %v", err)
	}
	if o := findCommentOutcome(t, resp2.TriggerOutcomes, nextAgentID); o.Status != DispatchQueued {
		t.Fatalf("hop 2 outcome = %+v, want queued (this is the VUH-96 symptom: blocked/invocation_not_allowed)", o)
	}
}

// TestCreateComment_CrossIssueChatMentionStaysUnattributed proves the MUL-4857
// guard is untouched: a speaking task actually bound to a DIFFERENT issue does
// not get the chat-task exemption — commenting here still stamps nothing, and
// the run it wakes stays unattributed.
func TestCreateComment_CrossIssueChatMentionStaysUnattributed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	speakerAgentID := createHandlerTestAgent(t, "Cross-Issue Speaker", nil)
	targetAgentID, ownerID, _ := privateAgentTestFixture(t)
	otherIssueID := createCommentTriggerPreviewIssue(t, "speaker's own issue", "", "")
	speakerTaskID := seedTaskOnIssueWithOriginator(t, speakerAgentID, otherIssueID, ownerID) // running on a DIFFERENT issue
	issueID := createCommentTriggerPreviewIssue(t, "cross-issue mention target", "", "")

	content := fmt.Sprintf("[@Target](mention://agent/%s) please", targetAgentID)
	r := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", map[string]any{"content": content}), "id", issueID)
	r.Header.Set("X-Agent-ID", speakerAgentID)
	r.Header.Set("X-Task-ID", speakerTaskID)
	w := httptest.NewRecorder()
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode comment: %v", err)
	}

	var sourceTaskID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT source_task_id FROM comment WHERE id = $1`, resp.ID).Scan(&sourceTaskID); err != nil {
		t.Fatalf("read comment source_task_id: %v", err)
	}
	if sourceTaskID.Valid {
		t.Fatalf("comment.source_task_id = %v, want NULL (speaker task is bound to a different issue, MUL-4857)", sourceTaskID)
	}

	var targetOriginator pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT originator_user_id FROM agent_task_queue
		WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'
	`, issueID, targetAgentID).Scan(&targetOriginator); err != nil {
		t.Fatalf("read target task: %v", err)
	}
	if targetOriginator.Valid {
		t.Fatalf("target task originator_user_id = %v, want NULL/unattributed (cross-issue lineage must fail closed)", targetOriginator)
	}
}

// TestAutopilotDelegationAuthority_ChatTaskStillReturnsEmpty proves the
// broader chat-task lineage fix does not relax MUL-4857's confused-deputy
// defense: a task bound to no issue still yields no delegation authority on
// an autopilot-origin issue.
func TestAutopilotDelegationAuthority_ChatTaskStillReturnsEmpty(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := "33333333-3333-3333-3333-333333333333"
	issue := db.Issue{
		ID:         util.MustParseUUID("44444444-4444-4444-4444-444444444444"),
		OriginType: pgtype.Text{String: "autopilot", Valid: true},
		OriginID:   util.MustParseUUID("55555555-5555-5555-5555-555555555555"),
	}
	chatTask := db.AgentTaskQueue{
		AgentID: util.MustParseUUID(agentID),
		IssueID: pgtype.UUID{}, // no issue: a chat/direct task
	}

	got := testHandler.autopilotDelegationAuthority(context.Background(), issue, "agent", agentID, chatTask)
	if got != "" {
		t.Fatalf("autopilotDelegationAuthority(chat task) = %q, want \"\" (MUL-4857 must not relax for a task bound to no issue)", got)
	}
}
