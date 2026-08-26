"use client";

import { useEffect, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Copy, GitBranch, Plus, RefreshCw, Trash2 } from "lucide-react";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  gitlabTargetsOptions,
  vcsConnectionsOptions,
  vcsKeys,
  vcsWebhookRegistrationsOptions,
} from "@multica/core/vcs";
import { api, ApiError } from "@multica/core/api";
import type { ConnectVCSResponse, GitLabTarget, VCSProvider, VCSWebhookRegistration } from "@multica/core/types";
import { useT } from "../../i18n";

const PROVIDERS: VCSProvider[] = ["forgejo", "gitea", "gitlab"];
const PROVIDER_LABELS: Record<VCSProvider, string> = {
  forgejo: "Forgejo",
  gitea: "Gitea",
  gitlab: "GitLab",
};
const PROVIDER_OPTIONS = PROVIDERS.map((p) => ({
  value: p,
  label: PROVIDER_LABELS[p],
}));

export function VCSTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();

  const { data } = useQuery(vcsConnectionsOptions(wsId));
  const connections = data?.connections ?? [];
  const configured = data?.configured === true;
  const canManage = data?.can_manage === true;
  const gitlabOAuth = data?.gitlab_oauth;

  const [provider, setProvider] = useState<VCSProvider>("forgejo");
  const [instanceUrl, setInstanceUrl] = useState("");
  const [token, setToken] = useState("");
  const [connecting, setConnecting] = useState(false);
  const [justConnected, setJustConnected] = useState<ConnectVCSResponse | null>(null);
  const [rotateTarget, setRotateTarget] = useState<string | null>(null);
  const [rotating, setRotating] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.get("gitlab_connected") === "1") {
      toast.success(t(($) => $.vcs.toast_gitlab_connected));
    }
    const gitlabError = params.get("gitlab_error");
    if (gitlabError) {
      toast.error(t(($) => $.vcs.toast_gitlab_error, { error: gitlabError }));
    }
  }, [t]);

  async function handleGitLabOAuth() {
    try {
      const result = await api.startGitLabOAuth(wsId);
      if (result.configured && result.url) window.location.href = result.url;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.vcs.toast_connect_failed));
    }
  }

  async function handleConnect() {
    if (connecting || !instanceUrl.trim() || !token.trim()) return;
    setConnecting(true);
    try {
      const resp = await api.connectVCS(wsId, {
        provider,
        instance_url: instanceUrl.trim(),
        access_token: token.trim(),
      });
      await qc.invalidateQueries({ queryKey: ["vcs", wsId] });
      setJustConnected(resp);
      setInstanceUrl("");
      setToken("");
      toast.success(t(($) => $.vcs.toast_connected));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.vcs.toast_connect_failed));
    } finally {
      setConnecting(false);
    }
  }

  async function handleRotateWebhook() {
    if (!rotateTarget || rotating) return;
    setRotating(true);
    try {
      const resp = await api.rotateVCSWebhook(wsId, rotateTarget);
      await qc.invalidateQueries({ queryKey: ["vcs", wsId] });
      setJustConnected(resp);
      setRotateTarget(null);
      toast.success(t(($) => $.vcs.toast_rotated));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.vcs.toast_rotate_failed));
    } finally {
      setRotating(false);
    }
  }

  async function handleDelete() {
    if (!deleteTarget || deleting) return;
    setDeleting(true);
    try {
      await api.deleteVCSConnection(wsId, deleteTarget);
      await qc.invalidateQueries({ queryKey: ["vcs", wsId] });
      if (justConnected?.id === deleteTarget) setJustConnected(null);
      toast.success(t(($) => $.vcs.toast_disconnected));
      setDeleteTarget(null);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.vcs.toast_disconnect_failed));
    } finally {
      setDeleting(false);
    }
  }

  async function copy(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      toast.success(t(($) => $.vcs.copied));
    } catch {
      toast.error(t(($) => $.vcs.copy_failed));
    }
  }

  return (
    <div className="space-y-6">
      <p className="text-body text-muted-foreground">{t(($) => $.vcs.page_description)}</p>

      {connections.length > 0 && (
        <div className="space-y-3">
          {connections.map((c) => (
            <Card key={c.id}>
              <CardContent className="space-y-4">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
                  <div className="flex min-w-0 items-start gap-3">
                    <div className="rounded-md border bg-muted/50 p-2 text-muted-foreground shrink-0">
                      <GitBranch className="h-4 w-4" />
                    </div>
                    <div className="min-w-0 space-y-0.5">
                      <p className="text-body font-medium break-all">
                        {(PROVIDER_LABELS[c.provider] ?? c.provider) + " · " + c.instance_url}
                      </p>
                      <p className="text-caption text-muted-foreground break-all">
                        {t(($) => $.vcs.connected_as, { login: c.account_login })}
                      </p>
                    </div>
                  </div>
                  {canManage && (
                    <div className="flex flex-wrap items-center gap-2 shrink-0">
                      {c.auth_kind === "oauth" && c.credential_status === "expired" && (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={handleGitLabOAuth}
                        >
                          {t(($) => $.vcs.reconnect_gitlab)}
                        </Button>
                      )}
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setRotateTarget(c.id)}
                      >
                        <RefreshCw className="h-3 w-3" />
                        {t(($) => $.vcs.regenerate_webhook)}
                      </Button>
                      <Button variant="outline" size="sm" onClick={() => setDeleteTarget(c.id)}>
                        <Trash2 className="h-3 w-3" />
                        {t(($) => $.vcs.disconnect)}
                      </Button>
                    </div>
                  )}
                </div>

                {c.auth_kind === "oauth" && c.credential_status === "expired" && (
                  <div className="flex items-center justify-between gap-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-caption text-destructive">
                    <p>{t(($) => $.vcs.reconnect_gitlab_prompt)}</p>
                    {canManage && (
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={handleGitLabOAuth}
                      >
                        {t(($) => $.vcs.reconnect_gitlab)}
                      </Button>
                    )}
                  </div>
                )}

                {c.auth_kind === "oauth" && (
                  <GitLabOAuthHooksSection
                    wsId={wsId}
                    connectionId={c.id}
                    canManage={canManage}
                    onReconnect={handleGitLabOAuth}
                  />
                )}
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {justConnected && (
        <Card className="border-primary/40">
          <CardContent className="space-y-3">
            <div className="space-y-1">
              <p className="text-body font-medium">{t(($) => $.vcs.webhook_setup_title)}</p>
              <p className="text-caption text-muted-foreground">
                {t(($) => $.vcs.webhook_setup_description)}
              </p>
            </div>
            <CopyField
              label={t(($) => $.vcs.webhook_url_label)}
              value={justConnected.webhook_url || justConnected.webhook_path}
              onCopy={copy}
              copyLabel={t(($) => $.vcs.copy)}
            />
            <CopyField
              label={t(($) => $.vcs.webhook_secret_label)}
              value={justConnected.webhook_secret}
              onCopy={copy}
              copyLabel={t(($) => $.vcs.copy)}
              mono
            />
            <p className="text-caption text-amber-600 dark:text-amber-500">
              {t(($) => $.vcs.webhook_secret_warning)}
            </p>
          </CardContent>
        </Card>
      )}

      {canManage && (
        <Card>
          <CardContent className="space-y-4">
            <p className="text-body font-medium">{t(($) => $.vcs.connect_title)}</p>
            {!configured ? (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.vcs.not_configured)}{" "}
                <code className="rounded bg-muted px-1 py-0.5 text-micro">
                  MULTICA_VCS_SECRET_KEY
                </code>
                .
              </p>
            ) : (
              <>
                {gitlabOAuth?.available && (
                  <Button className="w-full" onClick={handleGitLabOAuth}>
                    {t(($) => $.vcs.connect_gitlab)}
                  </Button>
                )}
                <div className="space-y-1.5">
                  <Label htmlFor="vcs-provider">{t(($) => $.vcs.form_provider_label)}</Label>
                  <Select
                    items={PROVIDER_OPTIONS}
                    value={provider}
                    onValueChange={(v) => setProvider(v as VCSProvider)}
                  >
                    <SelectTrigger id="vcs-provider" disabled={connecting}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {PROVIDERS.map((p) => (
                        <SelectItem key={p} value={p}>
                          {PROVIDER_LABELS[p]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="vcs-url">{t(($) => $.vcs.form_instance_url_label)}</Label>
                  <Input
                    id="vcs-url"
                    placeholder="https://forgejo.example.com"
                    value={instanceUrl}
                    onChange={(e) => setInstanceUrl(e.target.value)}
                    disabled={connecting}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="vcs-token">{t(($) => $.vcs.form_token_label)}</Label>
                  <Input
                    id="vcs-token"
                    type="password"
                    placeholder={t(($) => $.vcs.form_token_placeholder)}
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                    disabled={connecting}
                  />
                  <p className="text-caption text-muted-foreground">{t(($) => $.vcs.form_token_hint)}</p>
                </div>
                <div className="flex justify-end">
                  <Button
                    size="sm"
                    onClick={handleConnect}
                    disabled={connecting || !instanceUrl.trim() || !token.trim()}
                  >
                    {connecting ? t(($) => $.vcs.connecting) : t(($) => $.vcs.connect)}
                  </Button>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      )}

      {!canManage && connections.length === 0 && (
        <p className="text-caption text-muted-foreground">{t(($) => $.vcs.contact_admin)}</p>
      )}

      <AlertDialog
        open={!!rotateTarget}
        onOpenChange={(v) => {
          if (!v && !rotating) setRotateTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.vcs.rotate_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.vcs.rotate_confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={rotating}>
              {t(($) => $.vcs.rotate_confirm_cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleRotateWebhook} disabled={rotating}>
              {rotating ? t(($) => $.vcs.rotating) : t(($) => $.vcs.rotate_confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(v) => {
          if (!v && !deleting) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.vcs.disconnect_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.vcs.disconnect_confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>
              {t(($) => $.vcs.disconnect_confirm_cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete} disabled={deleting}>
              {deleting ? t(($) => $.vcs.disconnecting) : t(($) => $.vcs.disconnect_confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function is409(err: unknown): boolean {
  if (err instanceof ApiError && err.status === 409) return true;
  if (
    typeof err === "object" &&
    err !== null &&
    "status" in err &&
    (err as { status: unknown }).status === 409
  ) {
    return true;
  }
  return false;
}

function getErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (
    typeof err === "object" &&
    err !== null &&
    "message" in err &&
    typeof (err as { message: unknown }).message === "string"
  ) {
    return (err as { message: string }).message;
  }
  return String(err);
}

function GitLabOAuthHooksSection({
  wsId,
  connectionId,
  canManage,
  onReconnect,
}: {
  wsId: string;
  connectionId: string;
  canManage: boolean;
  onReconnect: () => void;
}) {
  const { t } = useT("settings");
  const qc = useQueryClient();
  const [mutatingTarget, setMutatingTarget] = useState<string | null>(null);

  const { data: hooksData, error: hooksError } = useQuery(
    vcsWebhookRegistrationsOptions(wsId, connectionId),
  );
  const registrations: VCSWebhookRegistration[] = hooksData?.registrations ?? [];

  const {
    data: targetsData,
    error: targetsError,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery(gitlabTargetsOptions(wsId, connectionId));

  const allProjects: GitLabTarget[] = targetsData?.pages.flatMap((p) => p.projects) ?? [];
  const allGroups: GitLabTarget[] = targetsData?.pages.flatMap((p) => p.groups) ?? [];

  async function handleAdd(scope: "project" | "group", targetId: number, targetPath: string) {
    const key = `${scope}-${targetId}`;
    setMutatingTarget(key);
    try {
      await api.createVCSWebhookRegistration(wsId, connectionId, {
        scope,
        target_id: targetId,
        target_path: targetPath,
      });
      await qc.invalidateQueries({ queryKey: vcsKeys.hooks(wsId, connectionId) });
      toast.success(t(($) => $.vcs.toast_webhook_created));
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        toast.error(t(($) => $.vcs.reconnect_gitlab_prompt), {
          action: {
            label: t(($) => $.vcs.reconnect_gitlab),
            onClick: onReconnect,
          },
        });
      } else if (err instanceof ApiError && err.status === 400) {
        toast.error(err.message);
      } else {
        toast.error(err instanceof Error ? err.message : t(($) => $.vcs.toast_webhook_create_failed));
      }
    } finally {
      setMutatingTarget(null);
    }
  }

  async function handleRemove(scope: "project" | "group", targetId: number) {
    const key = `${scope}-${targetId}`;
    setMutatingTarget(key);
    try {
      await api.deleteVCSWebhookRegistration(wsId, connectionId, scope, targetId);
      await qc.invalidateQueries({ queryKey: vcsKeys.hooks(wsId, connectionId) });
      toast.success(t(($) => $.vcs.toast_webhook_deleted));
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        toast.error(t(($) => $.vcs.reconnect_gitlab_prompt), {
          action: {
            label: t(($) => $.vcs.reconnect_gitlab),
            onClick: onReconnect,
          },
        });
      } else if (err instanceof ApiError && err.status === 400) {
        toast.error(err.message);
      } else {
        toast.error(err instanceof Error ? err.message : t(($) => $.vcs.toast_webhook_delete_failed));
      }
    } finally {
      setMutatingTarget(null);
    }
  }

  return (
    <div className="space-y-4 pt-2 border-t border-border/50">
      <div className="space-y-2">
        <p className="text-caption font-medium">{t(($) => $.vcs.registered_webhooks_title)}</p>
        {hooksError ? (
          is409(hooksError) ? (
            <div className="flex items-center justify-between gap-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-caption text-destructive">
              <p>{t(($) => $.vcs.reconnect_gitlab_prompt)}</p>
              {canManage && (
                <Button variant="destructive" size="sm" onClick={onReconnect}>
                  {t(($) => $.vcs.reconnect_gitlab)}
                </Button>
              )}
            </div>
          ) : (
            <p className="text-caption text-destructive">{getErrorMessage(hooksError)}</p>
          )
        ) : registrations.length === 0 ? (
          <p className="text-caption text-muted-foreground">{t(($) => $.vcs.no_webhooks_registered)}</p>
        ) : (
          <div className="space-y-1.5">
            {registrations.map((reg) => (
              <div
                key={`${reg.scope}-${reg.target_id}`}
                className="flex items-center justify-between gap-2 rounded-md border bg-muted/30 px-3 py-1.5 text-caption"
              >
                <div className="flex items-center gap-2 min-w-0">
                  <Badge variant="secondary" className="text-micro font-normal shrink-0">
                    {reg.scope === "project" ? t(($) => $.vcs.scope_project) : t(($) => $.vcs.scope_group)}
                  </Badge>
                  <span className="font-mono truncate">
                    {reg.target_path || reg.target_id}
                  </span>
                </div>
                {canManage && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2 text-destructive hover:text-destructive shrink-0"
                    onClick={() => handleRemove(reg.scope, reg.target_id)}
                    disabled={mutatingTarget === `${reg.scope}-${reg.target_id}`}
                    title={t(($) => $.vcs.remove_webhook)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    <span className="sr-only">{t(($) => $.vcs.remove_webhook)}</span>
                  </Button>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {canManage && (
        <div className="space-y-2">
          <p className="text-caption font-medium">{t(($) => $.vcs.target_picker_title)}</p>
          {targetsError ? (
            is409(targetsError) ? (
              <div className="flex items-center justify-between gap-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-caption text-destructive">
                <p>{t(($) => $.vcs.reconnect_gitlab_prompt)}</p>
                <Button variant="destructive" size="sm" onClick={onReconnect}>
                  {t(($) => $.vcs.reconnect_gitlab)}
                </Button>
              </div>
            ) : (
              <p className="text-caption text-destructive">{getErrorMessage(targetsError)}</p>
            )
          ) : (
            <div className="space-y-1.5">
              {allProjects.map((proj) => {
                const isRegistered = registrations.some(
                  (r) => r.scope === "project" && r.target_id === proj.id,
                );
                return (
                  <div
                    key={`project-${proj.id}`}
                    className="flex items-center justify-between gap-2 rounded-md border bg-card px-3 py-1.5 text-caption"
                  >
                    <div className="flex items-center gap-2 min-w-0">
                      <Badge variant="outline" className="text-micro font-normal shrink-0">
                        {t(($) => $.vcs.scope_project)}
                      </Badge>
                      <span className="truncate" title={proj.path_with_namespace}>
                        {proj.path_with_namespace}
                      </span>
                    </div>
                    {isRegistered ? (
                      <Button
                        variant="outline"
                        size="sm"
                        className="h-7 px-2 text-destructive hover:text-destructive shrink-0"
                        onClick={() => handleRemove("project", proj.id)}
                        disabled={mutatingTarget === `project-${proj.id}`}
                      >
                        {t(($) => $.vcs.remove_webhook)}
                      </Button>
                    ) : (
                      <Button
                        variant="outline"
                        size="sm"
                        className="h-7 px-2 shrink-0"
                        onClick={() => handleAdd("project", proj.id, proj.path_with_namespace)}
                        disabled={mutatingTarget === `project-${proj.id}`}
                      >
                        <Plus className="h-3.5 w-3.5 mr-1" />
                        {t(($) => $.vcs.add_webhook)}
                      </Button>
                    )}
                  </div>
                );
              })}

              {allGroups.map((grp) => {
                const isRegistered = registrations.some(
                  (r) => r.scope === "group" && r.target_id === grp.id,
                );
                return (
                  <div
                    key={`group-${grp.id}`}
                    className="flex items-center justify-between gap-2 rounded-md border bg-card px-3 py-1.5 text-caption"
                  >
                    <div className="flex items-center gap-2 min-w-0">
                      <Badge variant="outline" className="text-micro font-normal shrink-0">
                        {t(($) => $.vcs.scope_group)}
                      </Badge>
                      <span className="truncate" title={grp.path_with_namespace}>
                        {grp.path_with_namespace}
                      </span>
                    </div>
                    {isRegistered ? (
                      <Button
                        variant="outline"
                        size="sm"
                        className="h-7 px-2 text-destructive hover:text-destructive shrink-0"
                        onClick={() => handleRemove("group", grp.id)}
                        disabled={mutatingTarget === `group-${grp.id}`}
                      >
                        {t(($) => $.vcs.remove_webhook)}
                      </Button>
                    ) : (
                      <Button
                        variant="outline"
                        size="sm"
                        className="h-7 px-2 shrink-0"
                        onClick={() => handleAdd("group", grp.id, grp.path_with_namespace)}
                        disabled={mutatingTarget === `group-${grp.id}`}
                      >
                        <Plus className="h-3.5 w-3.5 mr-1" />
                        {t(($) => $.vcs.add_webhook)}
                      </Button>
                    )}
                  </div>
                );
              })}

              {hasNextPage && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="w-full text-caption"
                  onClick={() => fetchNextPage()}
                  disabled={isFetchingNextPage}
                >
                  {t(($) => $.vcs.load_more_targets)}
                </Button>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function CopyField({
  label,
  value,
  onCopy,
  copyLabel,
  mono,
}: {
  label: string;
  value: string;
  onCopy: (v: string) => void;
  copyLabel: string;
  mono?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <Label className="text-caption">{label}</Label>
      <div className="flex items-center gap-2">
        <Input
          readOnly
          value={value}
          className={mono ? "min-w-0 font-mono text-caption" : "min-w-0 text-caption"}
        />
        <Button
          variant="outline"
          size="sm"
          className="shrink-0"
          onClick={() => onCopy(value)}
          title={copyLabel}
        >
          <Copy className="h-3 w-3" />
        </Button>
      </div>
    </div>
  );
}
