import type { ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ListVCSConnectionsResponse, VCSConnection, VCSWebhookRegistration, GitLabTarget } from "@multica/core/types";
import { ApiError } from "@multica/core/api";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockConnectVCS = vi.hoisted(() => vi.fn());
const mockStartGitLabOAuth = vi.hoisted(() => vi.fn());
const mockCreateRegistration = vi.hoisted(() => vi.fn());
const mockDeleteRegistration = vi.hoisted(() => vi.fn());
const mockDeleteConnection = vi.hoisted(() => vi.fn());
const mockRotateWebhook = vi.hoisted(() => vi.fn());
const mockInvalidate = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

const connectionsRef = vi.hoisted(() => ({
  current: {
    connections: [] as VCSConnection[],
    available: true,
    configured: true,
    can_manage: true,
    gitlab_oauth: { available: true, instance_url: "https://gitlab.example.com" },
  } as ListVCSConnectionsResponse,
}));

const registrationsRef = vi.hoisted(() => ({
  current: [] as VCSWebhookRegistration[],
}));

const hooksErrorRef = vi.hoisted(() => ({
  current: null as unknown,
}));

const targetsRef = vi.hoisted(() => ({
  current: {
    pages: [
      {
        projects: [] as GitLabTarget[],
        groups: [] as GitLabTarget[],
        next_page: 0,
      },
    ],
  },
}));

const targetsErrorRef = vi.hoisted(() => ({
  current: null as unknown,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("connections")) return { data: connectionsRef.current, error: null };
    if (key.includes("hooks")) {
      return {
        data: hooksErrorRef.current ? undefined : { registrations: registrationsRef.current },
        error: hooksErrorRef.current,
      };
    }
    return { data: undefined, error: null };
  },
  useInfiniteQuery: (opts: { queryKey: unknown[] }) => {
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("targets")) {
      return {
        data: targetsErrorRef.current ? undefined : targetsRef.current,
        error: targetsErrorRef.current,
        fetchNextPage: vi.fn(),
        hasNextPage: false,
        isFetchingNextPage: false,
      };
    }
    return {
      data: undefined,
      error: null,
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
    };
  },
  useQueryClient: () => ({
    invalidateQueries: mockInvalidate,
  }),
  queryOptions: <T,>(opts: T) => opts,
  infiniteQueryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/api", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/api")>("@multica/core/api");
  return {
    ...actual,
    api: {
      connectVCS: mockConnectVCS,
      startGitLabOAuth: mockStartGitLabOAuth,
      createVCSWebhookRegistration: mockCreateRegistration,
      deleteVCSWebhookRegistration: mockDeleteRegistration,
      deleteVCSConnection: mockDeleteConnection,
      rotateVCSWebhook: mockRotateWebhook,
    },
  };
});

vi.mock("sonner", () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

import { VCSTab } from "./vcs-tab";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function resetFixtures() {
  vi.clearAllMocks();
  connectionsRef.current = {
    connections: [],
    available: true,
    configured: true,
    can_manage: true,
    gitlab_oauth: { available: true, instance_url: "https://gitlab.example.com" },
  };
  registrationsRef.current = [];
  hooksErrorRef.current = null;
  targetsRef.current = {
    pages: [
      {
        projects: [],
        groups: [],
        next_page: 0,
      },
    ],
  };
  targetsErrorRef.current = null;
}

describe("VCSTab", () => {
  beforeEach(resetFixtures);

  it("renders Connect GitLab button only when gitlab_oauth.available is true", () => {
    connectionsRef.current.gitlab_oauth = { available: true, instance_url: "https://gitlab.example.com" };
    render(<VCSTab />, { wrapper: I18nWrapper });
    expect(screen.getByRole("button", { name: /^Connect GitLab$/i })).toBeTruthy();
  });

  it("does not render Connect GitLab button when gitlab_oauth is unavailable", () => {
    connectionsRef.current.gitlab_oauth = { available: false, instance_url: "" };
    render(<VCSTab />, { wrapper: I18nWrapper });
    expect(screen.queryByRole("button", { name: /^Connect GitLab$/i })).toBeNull();
  });

  it("still renders PAT connect form for Forgejo/Gitea/GitLab fallback", () => {
    render(<VCSTab />, { wrapper: I18nWrapper });
    expect(screen.getByLabelText(/Instance URL/i)).toBeTruthy();
    expect(screen.getByLabelText(/Access token/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /^Connect$/i })).toBeTruthy();
  });

  it("renders the expired banner when credential_status is expired on an OAuth connection", async () => {
    const user = userEvent.setup();
    connectionsRef.current.connections = [
      {
        id: "conn-1",
        workspace_id: "workspace-1",
        provider: "gitlab",
        instance_url: "https://gitlab.example.com",
        account_login: "testuser",
        webhook_url: "https://multica.example.com/api/vcs/webhook/conn-1",
        webhook_path: "/api/vcs/webhook/conn-1",
        created_at: "2026-08-01T00:00:00Z",
        auth_kind: "oauth",
        credential_status: "expired",
      },
    ];
    mockStartGitLabOAuth.mockResolvedValue({ url: "https://gitlab.example.com/oauth/authorize", configured: true });

    render(<VCSTab />, { wrapper: I18nWrapper });
    expect(screen.getByText(/GitLab authorization expired\. Reconnect GitLab\./i)).toBeTruthy();

    const reconnectButtons = screen.getAllByRole("button", { name: /Reconnect GitLab/i });
    expect(reconnectButtons.length).toBeGreaterThan(0);
    await user.click(reconnectButtons[0]!);

    await waitFor(() => {
      expect(mockStartGitLabOAuth).toHaveBeenCalledWith("workspace-1");
    });
  });

  it("renders target picker and registered webhook list for an OAuth connection", () => {
    connectionsRef.current.connections = [
      {
        id: "conn-oauth-1",
        workspace_id: "workspace-1",
        provider: "gitlab",
        instance_url: "https://gitlab.example.com",
        account_login: "oauthuser",
        webhook_url: "https://multica.example.com/api/vcs/webhook/conn-oauth-1",
        webhook_path: "/api/vcs/webhook/conn-oauth-1",
        created_at: "2026-08-01T00:00:00Z",
        auth_kind: "oauth",
        credential_status: "ok",
      },
    ];
    registrationsRef.current = [
      {
        connection_id: "conn-oauth-1",
        scope: "project",
        target_id: 42,
        target_path: "org/project-a",
        hook_id: 101,
        created_at: "2026-08-01T00:00:00Z",
      },
    ];
    targetsRef.current = {
      pages: [
        {
          projects: [
            { id: 42, name: "project-a", path_with_namespace: "org/project-a", web_url: "https://gitlab.com/org/project-a" },
            { id: 43, name: "project-b", path_with_namespace: "org/project-b", web_url: "https://gitlab.com/org/project-b" },
          ],
          groups: [
            { id: 10, name: "group-a", path_with_namespace: "org/group-a", web_url: "https://gitlab.com/org/group-a" },
          ],
          next_page: 0,
        },
      ],
    };

    render(<VCSTab />, { wrapper: I18nWrapper });

    expect(screen.getByText("Registered webhooks")).toBeTruthy();
    expect(screen.getAllByText("org/project-a").length).toBe(2);
    expect(screen.getByText("Projects & Groups")).toBeTruthy();
    expect(screen.getByText("org/project-b")).toBeTruthy();
    expect(screen.getByText("org/group-a")).toBeTruthy();
  });

  it("add and remove fire the right client calls and invalidate hooks query", async () => {
    const user = userEvent.setup();
    connectionsRef.current.connections = [
      {
        id: "conn-oauth-1",
        workspace_id: "workspace-1",
        provider: "gitlab",
        instance_url: "https://gitlab.example.com",
        account_login: "oauthuser",
        webhook_url: "https://multica.example.com/api/vcs/webhook/conn-oauth-1",
        webhook_path: "/api/vcs/webhook/conn-oauth-1",
        created_at: "2026-08-01T00:00:00Z",
        auth_kind: "oauth",
        credential_status: "ok",
      },
    ];
    registrationsRef.current = [
      {
        connection_id: "conn-oauth-1",
        scope: "project",
        target_id: 42,
        target_path: "org/project-a",
        hook_id: 101,
        created_at: "2026-08-01T00:00:00Z",
      },
    ];
    targetsRef.current = {
      pages: [
        {
          projects: [
            { id: 42, name: "project-a", path_with_namespace: "org/project-a", web_url: "https://gitlab.com/org/project-a" },
            { id: 43, name: "project-b", path_with_namespace: "org/project-b", web_url: "https://gitlab.com/org/project-b" },
          ],
          groups: [],
          next_page: 0,
        },
      ],
    };
    mockCreateRegistration.mockResolvedValue({
      connection_id: "conn-oauth-1",
      scope: "project",
      target_id: 43,
      target_path: "org/project-b",
      hook_id: 102,
      created_at: "2026-08-01T00:00:00Z",
    });
    mockDeleteRegistration.mockResolvedValue(undefined);

    render(<VCSTab />, { wrapper: I18nWrapper });

    // Click Add webhook on project-b
    const addBtn = screen.getByRole("button", { name: /Add webhook/i });
    await user.click(addBtn);

    await waitFor(() => {
      expect(mockCreateRegistration).toHaveBeenCalledWith("workspace-1", "conn-oauth-1", {
        scope: "project",
        target_id: 43,
        target_path: "org/project-b",
      });
      expect(mockInvalidate).toHaveBeenCalledWith({
        queryKey: ["vcs", "workspace-1", "hooks", "conn-oauth-1"],
      });
      expect(mockToastSuccess).toHaveBeenCalledWith("Webhook registered");
    });

    // Click Remove on registered hook org/project-a
    const removeButtons = screen.getAllByRole("button", { name: /Remove/i });
    await user.click(removeButtons[0]!);

    await waitFor(() => {
      expect(mockDeleteRegistration).toHaveBeenCalledWith("workspace-1", "conn-oauth-1", "project", 42);
      expect(mockInvalidate).toHaveBeenCalledWith({
        queryKey: ["vcs", "workspace-1", "hooks", "conn-oauth-1"],
      });
      expect(mockToastSuccess).toHaveBeenCalledWith("Webhook removed");
    });
  });

  it("surfaces 400 and 409 errors correctly from hook operations", async () => {
    const user = userEvent.setup();
    connectionsRef.current.connections = [
      {
        id: "conn-oauth-1",
        workspace_id: "workspace-1",
        provider: "gitlab",
        instance_url: "https://gitlab.example.com",
        account_login: "oauthuser",
        webhook_url: "https://multica.example.com/api/vcs/webhook/conn-oauth-1",
        webhook_path: "/api/vcs/webhook/conn-oauth-1",
        created_at: "2026-08-01T00:00:00Z",
        auth_kind: "oauth",
        credential_status: "ok",
      },
    ];
    targetsRef.current = {
      pages: [
        {
          projects: [],
          groups: [
            { id: 99, name: "group-free", path_with_namespace: "org/group-free", web_url: "https://gitlab.com/org/group-free" },
          ],
          next_page: 0,
        },
      ],
    };
    mockCreateRegistration.mockRejectedValue(
      new ApiError("group webhooks require GitLab Premium", 400, "Bad Request"),
    );

    render(<VCSTab />, { wrapper: I18nWrapper });

    const addBtn = screen.getByRole("button", { name: /Add webhook/i });
    await user.click(addBtn);

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("group webhooks require GitLab Premium");
    });
  });

  it("renders inline reconnect prompt with working reconnect action when targets query fails with 409", async () => {
    const user = userEvent.setup();
    connectionsRef.current.connections = [
      {
        id: "conn-oauth-1",
        workspace_id: "workspace-1",
        provider: "gitlab",
        instance_url: "https://gitlab.example.com",
        account_login: "oauthuser",
        webhook_url: "https://multica.example.com/api/vcs/webhook/conn-oauth-1",
        webhook_path: "/api/vcs/webhook/conn-oauth-1",
        created_at: "2026-08-01T00:00:00Z",
        auth_kind: "oauth",
        credential_status: "ok",
      },
    ];
    targetsErrorRef.current = new ApiError(
      "GitLab authorization expired. Reconnect GitLab.",
      409,
      "Conflict",
    );
    mockStartGitLabOAuth.mockResolvedValue({
      url: "https://gitlab.example.com/oauth/authorize",
      configured: true,
    });

    render(<VCSTab />, { wrapper: I18nWrapper });

    expect(screen.getByText("Projects & Groups")).toBeTruthy();
    expect(screen.getByText(/GitLab authorization expired\. Reconnect GitLab\./i)).toBeTruthy();

    const reconnectBtn = screen.getByRole("button", { name: /Reconnect GitLab/i });
    await user.click(reconnectBtn);

    await waitFor(() => {
      expect(mockStartGitLabOAuth).toHaveBeenCalledWith("workspace-1");
    });
  });

  it("renders inline reconnect prompt with working reconnect action when registrations query fails with 409", async () => {
    const user = userEvent.setup();
    connectionsRef.current.connections = [
      {
        id: "conn-oauth-1",
        workspace_id: "workspace-1",
        provider: "gitlab",
        instance_url: "https://gitlab.example.com",
        account_login: "oauthuser",
        webhook_url: "https://multica.example.com/api/vcs/webhook/conn-oauth-1",
        webhook_path: "/api/vcs/webhook/conn-oauth-1",
        created_at: "2026-08-01T00:00:00Z",
        auth_kind: "oauth",
        credential_status: "ok",
      },
    ];
    hooksErrorRef.current = new ApiError(
      "GitLab authorization expired. Reconnect GitLab.",
      409,
      "Conflict",
    );
    mockStartGitLabOAuth.mockResolvedValue({
      url: "https://gitlab.example.com/oauth/authorize",
      configured: true,
    });

    render(<VCSTab />, { wrapper: I18nWrapper });

    expect(screen.getByText("Registered webhooks")).toBeTruthy();
    expect(screen.queryByText("No webhooks registered yet")).toBeNull();
    expect(screen.getByText(/GitLab authorization expired\. Reconnect GitLab\./i)).toBeTruthy();

    const reconnectBtn = screen.getByRole("button", { name: /Reconnect GitLab/i });
    await user.click(reconnectBtn);

    await waitFor(() => {
      expect(mockStartGitLabOAuth).toHaveBeenCalledWith("workspace-1");
    });
  });

  it("surfaces server message when targets query fails with non-409 error", () => {
    connectionsRef.current.connections = [
      {
        id: "conn-oauth-1",
        workspace_id: "workspace-1",
        provider: "gitlab",
        instance_url: "https://gitlab.example.com",
        account_login: "oauthuser",
        webhook_url: "https://multica.example.com/api/vcs/webhook/conn-oauth-1",
        webhook_path: "/api/vcs/webhook/conn-oauth-1",
        created_at: "2026-08-01T00:00:00Z",
        auth_kind: "oauth",
        credential_status: "ok",
      },
    ];
    targetsErrorRef.current = new ApiError("GitLab instance unreachable", 502, "Bad Gateway");

    render(<VCSTab />, { wrapper: I18nWrapper });

    expect(screen.getByText("Projects & Groups")).toBeTruthy();
    expect(screen.getByText("GitLab instance unreachable")).toBeTruthy();
  });

  it("surfaces server message when registrations query fails with non-409 error", () => {
    connectionsRef.current.connections = [
      {
        id: "conn-oauth-1",
        workspace_id: "workspace-1",
        provider: "gitlab",
        instance_url: "https://gitlab.example.com",
        account_login: "oauthuser",
        webhook_url: "https://multica.example.com/api/vcs/webhook/conn-oauth-1",
        webhook_path: "/api/vcs/webhook/conn-oauth-1",
        created_at: "2026-08-01T00:00:00Z",
        auth_kind: "oauth",
        credential_status: "ok",
      },
    ];
    hooksErrorRef.current = new ApiError("Failed to fetch webhook registrations", 500, "Internal Server Error");

    render(<VCSTab />, { wrapper: I18nWrapper });

    expect(screen.getByText("Registered webhooks")).toBeTruthy();
    expect(screen.queryByText("No webhooks registered yet")).toBeNull();
    expect(screen.getByText("Failed to fetch webhook registrations")).toBeTruthy();
  });
});
