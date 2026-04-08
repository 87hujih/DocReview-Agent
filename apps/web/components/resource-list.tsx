import Link from "next/link";

import type { Resource } from "../lib/api/resources";
import { toIsoSeconds, truncateId } from "../lib/terminal";
import { MetaRow } from "./ui/meta-row";
import styles from "./resource-list.module.css";

type ResourceListProps = {
  actionHrefBuilder?: (resource: Resource) => string;
  actionLabel?: string;
  emptyMessage?: string;
  resources: Resource[];
  selectedResourceId?: string;
};

export function ResourceList({
  actionHrefBuilder = (resource) => `/tasks/new?resource_id=${resource.id}`,
  actionLabel = "OPEN_TASK_CREATE",
  emptyMessage = "NO_RESOURCES_AVAILABLE",
  resources,
  selectedResourceId
}: ResourceListProps) {
  if (resources.length === 0) {
    return <p className={styles.empty}>{emptyMessage}</p>;
  }

  return (
    <div className={styles.list}>
      {resources.map((resource, index) => (
        <Link
          key={resource.id}
          className={styles.card}
          data-selected={resource.id === selectedResourceId}
          href={actionHrefBuilder(resource)}
        >
          <div className={styles.header}>
            <span className={styles.index}>RESOURCE {String(index + 1).padStart(2, "0")}</span>
            <span className={styles.action}>{actionLabel}</span>
          </div>

          <h3 className={styles.title}>{resource.title}</h3>

          <div className={styles.meta}>
            <MetaRow label="resource_id" value={truncateId(resource.id, 8, 4)} />
            <MetaRow label="source_type" value={resource.source_type} />
            <MetaRow label="created_at" value={toIsoSeconds(resource.created_at)} />
          </div>
        </Link>
      ))}
    </div>
  );
}
