package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/vcs"
)

func TestGitLabOAuthStateRoundTripAndValidation(t *testing.T) {
	h := testHandler
	if h == nil || testPool == nil || h.VCSSecretBox == nil {
		t.Skip("handler test fixtures unavailable")
	}
	state, err := h.sealVCSSecret(`{"workspace_id":"` + testWorkspaceID + `","verifier":"v","issued_at":` + "1" + `}`)
	if err != nil {
		t.Fatal("seal state:", err)
	}
	if _, err := h.openVCSSecret(state); err != nil {
		t.Fatal("state round trip:", err)
	}
	if _, err := h.openVCSSecret(state + "tampered"); err == nil {
		t.Fatal("tampered state must be rejected")
	}
}

func TestWriteGitLabHookErrorMappings(t *testing.T) {
	cases := []struct {
		name, scope  string
		status, want int
	}{
		{"group 403", "group", 403, 400},
		{"group 404", "group", 404, 400},
		{"project 403", "project", 403, 409},
		{"project 401", "project", 401, 409},
		{"other", "project", 500, 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeGitLabHookError(w, vcs.StatusError{Status: tc.status}, tc.scope)
			if w.Code != tc.want {
				t.Fatalf("%s: got %d, want %d", tc.name, w.Code, tc.want)
			}
		})
	}
}

func TestDeleteVCSWebhookRegistrationValidatesInputs(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixtures unavailable")
	}
	box := withVCSBox(t)
	connID := seedVCSConnection(t, context.Background(), box, "gitlab", testHandler.cfg.GitLabInstanceURL)
	t.Cleanup(func() { cleanupVCS(context.Background(), connID) })
	for name, path := range map[string]string{
		"invalid scope":  "/api/workspaces/" + testWorkspaceID + "/vcs/connections/" + connID + "/hooks/other/1",
		"invalid target": "/api/workspaces/" + testWorkspaceID + "/vcs/connections/" + connID + "/hooks/project/nope",
	} {
		t.Run(name, func(t *testing.T) {
			req := vcsHandlerRequest(http.MethodDelete, path, nil, connID, map[string]string{"scope": "other", "targetId": "nope"})
			w := httptest.NewRecorder()
			testHandler.DeleteVCSWebhookRegistration(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400, got %d", name, w.Code)
			}
		})
	}
}
