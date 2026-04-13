"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
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
import { getErrorMessage, isTerminalStatus, toIsoSeconds, truncateId } from "../../../lib/terminal";
import styles from "./page.module.css";

export default function TaskDetailPage() {
  const params = useParams<{ id: string }>();
  const taskId = typeof params.id === "string" ? params.id : "";

  const [artifacts, setArtifacts] = useState<TaskArtifact[]>([]);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [events, setEvents] = useState<TaskEvent[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [steps, setSteps] = useState<TaskStep[]>([]);
  const [task, setTask] = useState<Task | null>(null);

  // Effect 1: 初始加载，仅在 taskId 变化时触发
  useEffect(() => {
    if (!taskId) {
      setIsLoading(false);
      return;
    }

    let active = true;

    async function load() {
      try {
        const [taskResponse, taskArtifacts, taskEvents] = await Promise.all([
          getTask(taskId),
          getTaskArtifacts(taskId),
          getTaskEvents(taskId)
        ]);
        if (!active) return;
        setTask(taskResponse.task);
        setSteps(taskResponse.steps);
        setArtifacts(taskArtifacts);
        setEvents(taskEvents);
        setErrorMessage(null);
      } catch (error) {
        if (active) setErrorMessage(getErrorMessage(error));
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

    const timer = setInterval(() => {
      if (!active) return;

      void (async () => {
        try {
          const [taskResponse, taskArtifacts, taskEvents] = await Promise.all([
            getTask(taskId),
            getTaskArtifacts(taskId),
            getTaskEvents(taskId)
          ]);
          if (!active) return;
          setTask(taskResponse.task);
          setSteps(taskResponse.steps);
          setArtifacts(taskArtifacts);
          setEvents(taskEvents);
        } catch {
          // 轮询失败静默处理，下次间隔自动重试
        }
      })();
    }, 3000);

    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [taskStatus, taskId]);

  const citations = getCitationsArtifact(artifacts);
  const reviewSummary = getReviewSummaryArtifact(artifacts);
  const diffPreview = getDiffPreviewArtifact(artifacts);
  const hasCompletedResult = task?.status === "completed" && task.resource_id.trim() !== "";

  return (
    <div className={styles.page}>
      <TerminalFrame
        actions={<StatusChip status={task?.status || "pending"} />}
        label="任务会话"
        title="任务详情"
      >
        {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}

        {isLoading && !task ? (
          <p className={styles.placeholder}>正在加载任务会话</p>
        ) : (
          <div className={styles.meta}>
            <MetaRow label="任务状态" value={task ? task.status : "未知"} />
            <MetaRow label="id" value={task ? truncateId(task.id, 8, 4) : truncateId(taskId, 8, 4)} />
            <MetaRow label="资源 ID" value={task?.resource_id || "未知"} />
            <MetaRow label="创建时间" value={toIsoSeconds(task?.created_at)} />
            <MetaRow label="更新时间" value={toIsoSeconds(task?.updated_at)} />
            <MetaRow label="修订指令" value={task?.instruction || "未提供"} />
          </div>
        )}
      </TerminalFrame>

      {hasCompletedResult ? (
        <TerminalFrame
          label="修订结果"
          title="结果出口"
        >
          <p className={styles.resultText}>任务已完成，最终修订版本已写入资源库。</p>
          <div className={styles.resultActions}>
            <Link className={styles.resultLink} href={`/resources/${task.resource_id}`}>
              查看修订结果
            </Link>
            <Link className={styles.resultLink} href={getResourceExportURL(task.resource_id)}>
              下载修订结果
            </Link>
          </div>
        </TerminalFrame>
      ) : null}

      <TaskTimeline events={events} steps={steps} />

      {citations.length > 0 ? <CitationList citations={citations} /> : null}

      {reviewSummary ? (
        <TerminalFrame
          label="审阅摘要"
          title="审阅器输出"
        >
          <p className={styles.summary}>{reviewSummary.summary}</p>
        </TerminalFrame>
      ) : null}

      {diffPreview ? <DiffPreview sections={diffPreview.sections} /> : null}

      {!isLoading && citations.length === 0 && !reviewSummary && !diffPreview ? (
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
