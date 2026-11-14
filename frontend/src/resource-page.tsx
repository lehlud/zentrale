import type { ReactNode } from "react";

import { CONFIG, useAPI, useQuery } from "./api";
import { MoreButton, type Batch } from "./batches";
import type { Config } from "./config";
import { Empty, Loading, useAction } from "./ui";

/** Page actions share one pending/error channel; editor saves still reject. */
export function useResourcePage<T = Config>(query = CONFIG) {
  const api = useAPI();
  const result = useQuery<T>(query);
  const action = useAction();
  const mutate = (query: string, variables: Record<string, unknown> = {}) =>
    action.run(() => api.mutate(query, variables));

  return {
    ...result,
    api,
    error: result.error || action.error,
    busy: action.busy,
    run: action.run,
    mutate,
    remove: (kind: string, id: string) =>
      mutate(
        "mutation($kind:String!,$id:String!){deleteResource(kind:$kind,id:$id)}",
        { kind, id },
      ),
    retry: () => api.refresh(),
  };
}

type ResourceState = { data?: unknown; error: string; loading: boolean };

/** Initial failures are not empty collections. Keep pushed snapshots on errors. */
export function ResourceContent({
  resource,
  items,
  empty,
  children,
}: {
  resource: ResourceState;
  items: readonly unknown[] | undefined;
  empty?: ReactNode;
  children: ReactNode;
}) {
  if (!resource.data) {
    if (resource.loading) return <Loading />;
    if (resource.error) return null;
  }
  if (!items?.length) return empty ? <Empty>{empty}</Empty> : null;
  return children;
}

// The page owns the batch so tab/layout unmounts do not reset its visible count.
export function ResourceList<T>({
  resource,
  batch,
  empty,
  className,
  children,
}: {
  resource: ResourceState;
  batch: Batch<T>;
  empty?: ReactNode;
  className?: string;
  children: (item: T) => ReactNode;
}) {
  return (
    <ResourceContent resource={resource} items={batch.items} empty={empty}>
      <div className={className}>{batch.items.map(children)}</div>
      <MoreButton batch={batch} />
    </ResourceContent>
  );
}
