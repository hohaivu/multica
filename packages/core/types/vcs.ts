/**
 * Git provider integration types (Forgejo, Gitea, GitLab). GitLab supports an
 * optional deployment OAuth application; PAT connections remain supported.
 * Pull requests mirrored from any
 * of these providers surface through the shared GitHubPullRequest shape, tagged
 * with the matching `provider`.
 */

export type VCSProvider = "forgejo" | "gitea" | "gitlab";

export interface VCSConnection {
  id: string;
  workspace_id: string;
  provider: VCSProvider;
  /** Instance base URL, e.g. https://forgejo.example.com (no trailing slash). */
  instance_url: string;
  /** Login (user or org) the stored access token authenticates as. */
  account_login: string;
  /** Absolute webhook endpoint to register on the provider. Empty when the server
   * has no public URL configured; the UI then prefixes `webhook_path`. */
  webhook_url: string;
  webhook_path: string;
  created_at: string;
  auth_kind: "pat" | "oauth";
  credential_status: "ok" | "expired";
}

export interface ListVCSConnectionsResponse {
  connections: VCSConnection[];
  /** Whether this deployment offers the integration at all (self-host only;
   * off on the managed cloud). When false the section is hidden entirely.
   * Older backends omit it; treat as true so the existing self-host UI still
   * renders (visibility is also gated by vcs_integration_available on
   * /api/config, which is the authoritative deployment signal). */
  available?: boolean;
  /** Whether the deployment has MULTICA_VCS_SECRET_KEY configured. When false
   * the connect form is disabled. Older backends omit it; treat as false. */
  configured?: boolean;
  /** Whether the caller can connect / disconnect. Non-admins get false. */
  can_manage?: boolean;
  gitlab_oauth?: GitLabOAuthAvailability;
}

export interface GitLabOAuthAvailability { available: boolean; instance_url: string }
export interface GitLabTarget { id: number; name: string; path_with_namespace: string; web_url: string }
export interface VCSWebhookRegistration { connection_id: string; scope: "project" | "group"; target_id: number; target_path: string; hook_id: number; created_at: string }

export interface ConnectVCSRequest {
  provider: VCSProvider;
  instance_url: string;
  access_token: string;
}

export interface ConnectVCSResponse extends VCSConnection {
  /** One-time plaintext webhook secret to paste into the provider (HMAC secret
   * for Forgejo/Gitea, X-Gitlab-Token value for GitLab). Not retrievable
   * afterwards (stored encrypted); reconnecting rotates it. */
  webhook_secret: string;
}
