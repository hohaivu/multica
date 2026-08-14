package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestListInboxItemsRespectsLimit pins the LIMIT 200 on ListInboxItems (#6527).
// Without the cap, a heavy inbox returns every non-archived row — multi-MB
// payloads on every mark-read refetch. The archived list is already capped at
// 200 (ListArchivedInboxItems); this test proves the active list is too.
func TestListInboxItemsRespectsLimit(t *testing.T) {
	queries := db.New(testPool)
	ctx := context.Background()

	const insertCount = 205
	recipientEmail := "inbox-limit-test@multica.ai"
	recipientID := createTestUser(t, recipientEmail)
	t.Cleanup(func() { cleanupTestUser(t, recipientEmail) })
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM inbox_item WHERE recipient_id = $1`, util.MustParseUUID(recipientID))
	})

	workspaceID := util.MustParseUUID(testWorkspaceID)
	userID := util.MustParseUUID(recipientID)
	_, err := testPool.Exec(ctx, `
		INSERT INTO inbox_item (
			workspace_id, recipient_type, recipient_id, type, severity, title,
			read, archived, created_at
		)
		SELECT $1, 'member', $2, 'issue_assigned', 'info',
			       'limit test item ' || ordinal, false, false,
			       TIMESTAMPTZ '2025-01-01 00:00:00+00' + ordinal * INTERVAL '1 second'
		FROM generate_series(0, $3 - 1) AS ordinal
	`, workspaceID, userID, insertCount)
	if err != nil {
		t.Fatalf("insert %d inbox items: %v", insertCount, err)
	}

	items, err := queries.ListInboxItems(ctx, db.ListInboxItemsParams{
		WorkspaceID:   workspaceID,
		RecipientType: "member",
		RecipientID:   userID,
	})
	if err != nil {
		t.Fatalf("ListInboxItems: %v", err)
	}

	if len(items) != 200 {
		t.Fatalf("ListInboxItems returned %d items, want 200 (LIMIT)", len(items))
	}
	if got := items[0].Title; got != "limit test item 204" {
		t.Errorf("newest item = %q, want ordinal 204", got)
	}
	if got := items[len(items)-1].Title; got != "limit test item 5" {
		t.Errorf("oldest retained item = %q, want ordinal 5", got)
	}
	excluded := make(map[string]struct{}, 5)
	for ordinal := 0; ordinal < 5; ordinal++ {
		excluded[fmt.Sprintf("limit test item %d", ordinal)] = struct{}{}
	}
	for _, item := range items {
		if _, ok := excluded[item.Title]; ok {
			t.Errorf("oldest excluded item %q was returned", item.Title)
		}
	}

	unread, err := queries.CountUnreadInbox(ctx, db.CountUnreadInboxParams{
		WorkspaceID:   workspaceID,
		RecipientType: "member",
		RecipientID:   userID,
	})
	if err != nil {
		t.Fatalf("CountUnreadInbox: %v", err)
	}
	if unread != insertCount {
		t.Errorf("CountUnreadInbox = %d, want %d", unread, insertCount)
	}
}
