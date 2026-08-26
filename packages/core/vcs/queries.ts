import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const vcsKeys = {
  all: (wsId: string) => ["vcs", wsId] as const,
  connections: (wsId: string) => [...vcsKeys.all(wsId), "connections"] as const,
  targets: (wsId: string, connectionId: string) => [...vcsKeys.all(wsId), "targets", connectionId] as const,
  hooks: (wsId: string, connectionId: string) => [...vcsKeys.all(wsId), "hooks", connectionId] as const,
};

export const vcsConnectionsOptions = (wsId: string) =>
  queryOptions({
    queryKey: vcsKeys.connections(wsId),
    queryFn: () => api.listVCSConnections(wsId),
    enabled: !!wsId,
  });

export const gitlabTargetsOptions = (wsId: string, connectionId: string) =>
  infiniteQueryOptions({
    queryKey: vcsKeys.targets(wsId, connectionId),
    queryFn: ({ pageParam }) =>
      api.listGitLabTargets(wsId, connectionId, {
        page: pageParam,
      }),
    initialPageParam: 1,
    getNextPageParam: (lastPage) => (lastPage.next_page ? lastPage.next_page : undefined),
    enabled: !!wsId && !!connectionId,
  });

export const vcsWebhookRegistrationsOptions = (wsId: string, connectionId: string) =>
  queryOptions({
    queryKey: vcsKeys.hooks(wsId, connectionId),
    queryFn: () => api.listVCSWebhookRegistrations(wsId, connectionId),
    enabled: !!wsId && !!connectionId,
  });
