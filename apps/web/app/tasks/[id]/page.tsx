"use client";

import Link from "next/link";
import { useParams, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";

import { CitationList } from "../../../components/citation-list";
import { DiffPreview } from "../../../components/diff-preview";
import { TaskTimeline } from "../../../components/task-timeline";
import { MetaRow } from "../../../components/ui/meta-row";
import { StatusChip } from "../../../components/ui/status-chip";
import { TerminalFrame } from "../../../components/ui/terminal-frame";
import {
  getCitationsArtifact,
  getDiffPreviewArtifact,
  getReviewSummaryArtifact,
  getTask,
  getTaskArtifacts,
  getTaskEvents,
  type Task,
  type TaskArtifact,
  type TaskEvent,
  type TaskStep
} from "../../../lib/api/tasks";
import { getResourceExportURL } from "../../../lib/api/resources";
import {
  formatStatusLabel,
  getErrorMessage,
  isTerminalStatus,
  toIsoSeconds,
  truncateId
} from "../../../lib/terminal";
import styles from "./page.module.css";

export default function TaskDetailPage() {
  const params = useParams<{ id: string }>();
  const searchParams = useSearchParams();
  const taskId = typeof params.id === "string" ? params.id : "";
  const sessionId = normalizeSessionId(searchParams.get("session"));

  const [artifacts, setArtifacts] = useState<TaskArtifact[]>([]);
  const [artifactsError, setArtifactsError] = useState<string | null>(null);
  const [artifactsLoaded, setArtifactsLoaded] = useState(false);
  const [events, setEvents] = useState<TaskEvent[]>([]);
  const [eventsError, setEventsError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [steps, setSteps] = useState<TaskStep[]>([]);
  const [task, setTask] = useState<Task | null>(null);
  const [taskError, setTaskError] = useState<string | null>(null);

  function applyTaskResponse(taskResponse: { task: Task; steps: TaskStep[] }) {
    setTask(taskResponse.task);
    setSteps(taskResponse.steps);
    setTaskError(null);
  }

  function applyArtifactsSuccess(taskArtifacts: TaskArtifact[]) {
    setArtifacts(taskArtifacts);
    setArtifactsLoaded(true);
    setArtifactsError(null);
  }

  function applyArtifactsFailure(error: unknown) {
    setArtifactsError(getErrorMessage(error));
  }

  function applyEventsSuccess(taskEvents: TaskEvent[]) {
    setEvents(taskEvents);
    setEventsError(null);
  }

  function applyEventsFailure(error: unknown) {
    setEventsError(getErrorMessage(error));
  }

  // Effect 1: 初始加载，仅在 taskId 变化时触发
  useEffect(() => {
    if (!taskId) {
      setIsLoading(false);
      setTask(null);
      setSteps([]);
      setArtifacts([]);
      setEvents([]);
      setTaskError(null);
      setArtifactsError(null);
      setEventsError(null);
      setArtifactsLoaded(false);
      return;
    }

    let active = true;

    async function load() {
      setIsLoading(true);
      setTask(null);
      setSteps([]);
      setArtifacts([]);
      setEvents([]);
      setTaskError(null);
      setArtifactsError(null);
      setEventsError(null);
      setArtifactsLoaded(false);

      try {
        const taskResponse = await getTask(taskId);
        if (!active) return;
        applyTaskResponse(taskResponse);

        const [taskArtifacts, taskEvents] = await Promise.allSettled([
          getTaskArtifacts(taskId),
          getTaskEvents(taskId)
        ]);
        if (!active) return;

        if (taskArtifacts.status === "fulfilled") {
          applyArtifactsSuccess(taskArtifacts.value);
        } else {
          applyArtifactsFailure(taskArtifacts.reason);
        }

        if (taskEvents.status === "fulfilled") {
          applyEventsSuccess(taskEvents.value);
        } else {
          applyEventsFailure(taskEvents.reason);
        }
      } catch (error) {
        if (active) {
          setTaskError(getErrorMessage(error));
        }
      } finally {
        if (active) setIsLoading(false);
      }
    }

    void load();

    return () => {
      active = false;
    };
  }, [taskId]);

  // Effect 2: 轮询，仅在初始加载完成且任务处于非终态时运行
  const taskStatus = task?.status ?? null;

  useEffect(() => {
    if (!taskId || taskStatus === null || isTerminalStatus(taskStatus)) return;

    let active = true;
    let timer: ReturnType<typeof setTimeout> | null = null;

    async function pollOnce() {
      try {
        const taskResponse = await getTask(taskId);
        if (!active) return;
        applyTaskResponse(taskResponse);

        const [taskArtifacts, taskEvents] = await Promise.allSettled([
          getTaskArtifacts(taskId),
          getTaskEvents(taskId)
        ]);
        if (!active) return;

        if (taskArtifacts.status === "fulfilled") {
          applyArtifactsSuccess(taskArtifacts.value);
        } else {
          applyArtifactsFailure(taskArtifacts.reason);
        }

        if (taskEvents.status === "fulfilled") {
          applyEventsSuccess(taskEvents.value);
        } else {
          applyEventsFailure(taskEvents.reason);
        }
      } catch (error) {
        if (active) {
          setTaskError(getErrorMessage(error));
        }
      } finally {
        if (active) {
          timer = setTimeout(() => {
            void pollOnce();
          }, 3000);
        }
      }
    }

    timer = setTimeout(() => {
      void pollOnce();
    }, 3000);

    return () => {
      active = false;
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [taskStatus, taskId]);

  const citations = getCitationsArtifact(artifacts);
  const reviewSummary = getReviewSummaryArtifact(artifacts);
  const diffPreview = getDiffPreviewArtifact(artifacts);
  const isCompletedTask = task?.status === "completed";
  const hasResultResource = isCompletedTask && Boolean(task?.resource_id.trim());
  const effectiveSessionId = sessionId ?? normalizeSessionId(task?.source_session_id ?? null);
  const assistantHref = buildAssistantSessionHref(effectiveSessionId);
  const assistantLabel = effectiveSessionId ? "返回原会话" : "返回助手";
  const resultHref =
    hasResultResource && task ? withSessionQuery(`/resources/${task.resource_id}`, effectiveSessionId) : null;
  const showArtifactsPlaceholder =
    artifactsLoaded && citations.length === 0 && !reviewSummary && !diffPreview && !isCompletedTask;
  const showErrorOnly = !isLoading && !task && Boolean(taskError);
  const showTimeline = !showErrorOnly && (!isCompletedTask || steps.length > 0 || events.length > 0);

  return (
    <div className={styles.page}>
      <TerminalFrame
        actions={showErrorOnly ? undefined : <StatusChip status={task?.status || "pending"} />}
        label="任务会话"
        title="任务详情"
      >
        {taskError ? <p className={styles.error}>错误 &gt; {taskError}</p> : null}

        {isLoading && !task ? (
          <p className={styles.placeholder}>正在加载任务会话</p>
        ) : !showErrorOnly ? (
          <div className={styles.meta}>
            <MetaRow label="任务状态" value={formatStatusLabel(task?.status)} />
            <MetaRow label="id" value={task ? truncateId(task.id, 8, 4) : truncateId(taskId, 8, 4)} />
            <MetaRow label="资源 ID" value={task?.resource_id || "未知"} />
            <MetaRow label="创建时间" value={toIsoSeconds(task?.created_at)} />
            <MetaRow label="更新时间" value={toIsoSeconds(task?.updated_at)} />
            <MetaRow label="修订指令" value={task?.instruction || "未提供"} />
          </div>
        ) : null}
      </TerminalFrame>

      {!showErrorOnly && isCompletedTask ? (
        <TerminalFrame
          label="完成状态"
          title="流程已完成"
        >
          <p className={styles.resultText}>任务流程已执行完成，可以返回助手继续下一步，或查看修订结果。</p>
          <div className={styles.resultActions}>
            <Link className={styles.resultLink} href={assistantHref}>
              {assistantLabel}
            </Link>
            {resultHref ? (
              <Link className={styles.resultLink} href={resultHref}>
                查看修订结果
              </Link>
            ) : null}
            {hasResultResource ? (
              <Link className={styles.resultLink} href={getResourceExportURL(task.resource_id)}>
                下载修订结果
              </Link>
            ) : null}
          </div>
        </TerminalFrame>
      ) : null}

      {!showErrorOnly && eventsError ? <p className={styles.error}>事件流错误 &gt; {eventsError}</p> : null}
      {showTimeline ? <TaskTimeline events={events} steps={steps} /> : null}

      {!showErrorOnly && artifactsError ? <p className={styles.error}>产物错误 &gt; {artifactsError}</p> : null}
      {!showErrorOnly && citations.length > 0 ? <CitationList citations={citations} /> : null}

      {!showErrorOnly && reviewSummary ? (
        <TerminalFrame
          label="审阅摘要"
          title="审阅器输出"
        >
          <p className={styles.summary}>{reviewSummary.summary}</p>
        </TerminalFrame>
      ) : null}

      {!showErrorOnly && diffPreview ? <DiffPreview sections={diffPreview.sections} /> : null}

      {!showErrorOnly && !isLoading && showArtifactsPlaceholder ? (
        <TerminalFrame
          label="产物"
          title="等待产物生成"
        >
          <p className={styles.placeholder}>当前还没有可展示的产物</p>
        </TerminalFrame>
      ) : null}
    </div>
  );
}

function normalizeSessionId(value: string | null): string | null {
  const normalized = (value || "").trim();
  return normalized || null;
}

function buildAssistantSessionHref(sessionId: string | null): string {
  if (!sessionId) {
    return "/";
  }

  return `/?session=${encodeURIComponent(sessionId)}`;
}

function withSessionQuery(pathname: string, sessionId: string | null): string {
  if (!sessionId) {
    return pathname;
  }

  return `${pathname}?session=${encodeURIComponent(sessionId)}`;
}
