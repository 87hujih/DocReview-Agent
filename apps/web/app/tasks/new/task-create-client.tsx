"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { ResourceSearch } from "../../../components/resource-search";
import {
  TaskCreateForm,
  type TaskCreateFormResource
} from "../../../components/task-create-form";
import { MetaRow } from "../../../components/ui/meta-row";
import { TerminalFrame } from "../../../components/ui/terminal-frame";
import { getResource } from "../../../lib/api/resources";
import { createTask } from "../../../lib/api/tasks";
import { getErrorMessage } from "../../../lib/terminal";
import styles from "./page.module.css";

type TaskCreatePageClientProps = {
  resourceId: string;
};

type VersionSummary = {
  source: string;
  versionNumber: number;
};

export default function TaskCreatePageClient({ resourceId }: TaskCreatePageClientProps) {
  const router = useRouter();

  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(Boolean(resourceId));
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [resource, setResource] = useState<TaskCreateFormResource | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [versionSummary, setVersionSummary] = useState<VersionSummary | null>(null);

  useEffect(() => {
    if (!resourceId) {
      setIsLoading(false);
      setResource(null);
      setVersionSummary(null);
      return;
    }

    let active = true;
    setIsLoading(true);

    async function loadResource() {
      try {
        const response = await getResource(resourceId);
        if (!active) {
          return;
        }

        setResource({
          createdAt: response.resource.created_at,
          id: response.resource.id,
          sourceType: response.resource.source_type,
          title: response.resource.title
        });
        setVersionSummary(
          response.current_version
            ? {
                source: response.current_version.source,
                versionNumber: response.current_version.version_number
              }
            : null
        );
        setErrorMessage(null);
      } catch (error) {
        if (active) {
          setErrorMessage(getErrorMessage(error));
          setResource(null);
          setVersionSummary(null);
        }
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    }

    void loadResource();

    return () => {
      active = false;
    };
  }, [resourceId]);

  async function handleSubmit(instruction: string) {
    if (!resourceId) {
      return;
    }

    setIsSubmitting(true);
    setSubmitError(null);

    try {
      const task = await createTask({ instruction, resource_id: resourceId });
      router.push(`/tasks/${task.id}`);
    } catch (error) {
      setSubmitError(getErrorMessage(error));
    } finally {
      setIsSubmitting(false);
    }
  }

  const showResourcePanels = Boolean(resourceId) && !isLoading && Boolean(resource);

  return (
    <div className={styles.page}>
      {!resourceId ? (
        <TerminalFrame
          label="创建任务"
          title="未选择资源"
        >
          <Link className={styles.linkButton} href="/resources">
            前往资源库
          </Link>
        </TerminalFrame>
      ) : null}

      {resourceId ? (
        <TerminalFrame
          label="资源上下文"
          title="已选资源"
        >
          {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}

          {isLoading ? (
            <p className={styles.placeholder}>正在加载资源上下文</p>
          ) : resource ? (
            <div className={styles.meta}>
              <MetaRow label="资源 ID" value={resource.id} />
              <MetaRow label="资源标题" value={resource.title} />
              <MetaRow label="来源类型" value={resource.sourceType} />
              <MetaRow
                label="当前版本"
                value={
                  versionSummary
                    ? `版本 ${versionSummary.versionNumber} / ${versionSummary.source}`
                    : "未找到版本信息"
                }
              />
            </div>
          ) : errorMessage ? (
            <Link className={styles.linkButton} href="/resources">
              返回资源库
            </Link>
          ) : null}
        </TerminalFrame>
      ) : null}

      {showResourcePanels ? (
        <TaskCreateForm
          errorMessage={submitError}
          isSubmitting={isSubmitting}
          onSubmit={handleSubmit}
          resource={resource}
        />
      ) : null}

      {showResourcePanels ? <ResourceSearch resourceId={resourceId} /> : null}
    </div>
  );
}
