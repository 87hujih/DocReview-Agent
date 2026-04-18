"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";

import { getFileDownloadURL } from "../../lib/api/files";
import { formatStatusLabel } from "../../lib/terminal";
import type { AssistantRenderableMessage } from "../../lib/assistant/types";
import { AssistantMarkdown } from "./assistant-markdown";
import styles from "./assistant-message-list.module.css";

type AssistantMessageListProps = {
  activeTaskSuggestionId: string | null;
  messages: AssistantRenderableMessage[];
  onConfirmTaskSuggestion: (messageId: string) => Promise<void> | void;
  onStopGeneration?: () => void;
  showStopAction?: boolean;
  stopActionLabel?: string;
};

type CopyState = "error" | "success";

function formatTime(isoString: string): string {
  const date = new Date(isoString);
  if (isNaN(date.getTime())) return "";
  const h = String(date.getHours()).padStart(2, "0");
  const m = String(date.getMinutes()).padStart(2, "0");
  return `${h}:${m}`;
}

function collectConsumedSuggestionIds(messages: AssistantRenderableMessage[]): Set<string> {
  const consumedSuggestionIds = new Set<string>();

  for (const message of messages) {
    if (message.kind === "task_created") {
      consumedSuggestionIds.add(message.payload.suggestion_message_id);
    }
  }

  return consumedSuggestionIds;
}

export function AssistantMessageList({
  activeTaskSuggestionId,
  messages,
  onConfirmTaskSuggestion,
  onStopGeneration,
  showStopAction = false,
  stopActionLabel = "停止生成"
}: AssistantMessageListProps) {
  const consumedSuggestionIds = collectConsumedSuggestionIds(messages);
  const [copyStates, setCopyStates] = useState<Record<string, CopyState>>({});
  const copyResetTimers = useRef<Record<string, ReturnType<typeof setTimeout>>>({});

  useEffect(() => {
    return () => {
      Object.values(copyResetTimers.current).forEach((timerId) => clearTimeout(timerId));
      copyResetTimers.current = {};
    };
  }, []);

  if (messages.length === 0) {
    return (
      <div className={styles.emptyState}>
        <p className={styles.emptyEyebrow}>个人助手</p>
        <h2 className={styles.emptyTitle}>有什么可以帮到你？</h2>
        <p className={styles.emptyText}>输入你的问题或需求，我来为你解答和整理。</p>
      </div>
    );
  }

  function scheduleCopyReset(messageId: string) {
    if (copyResetTimers.current[messageId]) {
      clearTimeout(copyResetTimers.current[messageId]);
    }

    copyResetTimers.current[messageId] = setTimeout(() => {
      setCopyStates((current) => {
        const next = { ...current };
        delete next[messageId];
        return next;
      });
      delete copyResetTimers.current[messageId];
    }, 1600);
  }

  async function handleCopy(messageId: string, content: string) {
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error("clipboard unavailable");
      }

      await navigator.clipboard.writeText(content);
      setCopyStates((current) => ({ ...current, [messageId]: "success" }));
    } catch {
      setCopyStates((current) => ({ ...current, [messageId]: "error" }));
    }

    scheduleCopyReset(messageId);
  }

  function getCopyLabel(messageId: string): string {
    const state = copyStates[messageId];
    if (state === "success") {
      return "已复制";
    }

    if (state === "error") {
      return "复制失败";
    }

    return "复制";
  }

  return (
    <div className={styles.list}>
      {messages.map((message) => {
        if (message.kind === "task_suggestion") {
          const isConsumed = consumedSuggestionIds.has(message.id);
          const isCreating = activeTaskSuggestionId === message.id;

          return (
            <section key={message.id} className={styles.card}>
              <div className={styles.labelRow}>
                <p className={styles.cardLabel}>任务建议</p>
                <time className={styles.timestamp} dateTime={message.created_at}>
                  {formatTime(message.created_at)}
                </time>
              </div>
              <h3 className={styles.cardTitle}>{message.payload.title}</h3>
              <p className={styles.cardBody}>{message.payload.instruction}</p>
              <p className={styles.cardMeta}>{message.payload.resource_label}</p>
              <p className={styles.cardStatus}>{message.payload.status_message}</p>
              <button
                disabled={!message.payload.can_create || isCreating || isConsumed}
                onClick={() => void onConfirmTaskSuggestion(message.id)}
                type="button"
              >
                {isCreating ? "正在创建任务" : isConsumed ? "任务已创建" : message.payload.action_label}
              </button>
            </section>
          );
        }

        if (message.kind === "task_created") {
          return (
            <section key={message.id} className={styles.card}>
              <div className={styles.labelRow}>
                <p className={styles.cardLabel}>任务已创建</p>
                <time className={styles.timestamp} dateTime={message.created_at}>
                  {formatTime(message.created_at)}
                </time>
              </div>
              <h3 className={styles.cardTitle}>{message.payload.instruction}</h3>
              <p className={styles.cardMeta}>任务状态：{formatStatusLabel(message.payload.status)}</p>
              <Link className={styles.linkButton} href={message.payload.detail_url}>
                打开任务详情
              </Link>
            </section>
          );
        }

        if (message.kind === "task_status") {
          return (
            <section key={message.id} className={styles.card}>
              <div className={styles.labelRow}>
                <p className={styles.cardLabel}>任务结果</p>
                <time className={styles.timestamp} dateTime={message.created_at}>
                  {formatTime(message.created_at)}
                </time>
              </div>
              <h3 className={styles.cardTitle}>{message.payload.title}</h3>
              <p className={styles.cardBody}>{message.payload.instruction}</p>
              <p className={styles.cardMeta}>任务状态：{formatStatusLabel(message.payload.status)}</p>
              <p className={styles.cardStatus}>{message.payload.status_message}</p>
              {message.payload.result_url ? (
                <Link className={styles.linkButton} href={message.payload.result_url}>
                  查看修订结果
                </Link>
              ) : null}
              <Link className={styles.linkButton} href={message.payload.detail_url}>
                打开任务详情
              </Link>
            </section>
          );
        }

        if (message.kind === "session_file") {
          return (
            <section key={message.id} className={styles.card}>
              <div className={styles.labelRow}>
                <p className={styles.cardLabel}>会话文件</p>
                <time className={styles.timestamp} dateTime={message.created_at}>
                  {formatTime(message.created_at)}
                </time>
              </div>
              <h3 className={styles.cardTitle}>{message.payload.file_name}</h3>
              <p className={styles.cardMeta}>
                已自动入库：{message.payload.resource_title} · {message.payload.source_type}
              </p>
              {message.payload.file_id ? (
                <Link className={styles.linkButton} href={getFileDownloadURL(message.payload.file_id)}>
                  下载原文件
                </Link>
              ) : null}
            </section>
          );
        }

        if (message.kind === "system") {
          return (
            <article key={message.id} className={styles.systemMessage}>
              <p className={styles.systemText}>{message.payload.content}</p>
            </article>
          );
        }

        if (message.kind === "local_error") {
          return (
            <article key={message.id} className={styles.systemMessage}>
              <p className={styles.systemText}>{message.payload.content}</p>
            </article>
          );
        }

        const isStreamingAssistant = message.kind === "local_text" && message.role === "assistant" && message.local_state === "streaming";
        const content = message.payload.content;

        return (
          <div key={message.id} className={styles.messageRow} data-role={message.role}>
            <article className={styles.message} data-role={message.role}>
              <div className={styles.messageHeader}>
                <p className={styles.role}>{message.role === "assistant" ? "助手" : "你"}</p>
                <div className={styles.messageActions}>
                  <time className={styles.timestamp} dateTime={message.created_at}>
                    {formatTime(message.created_at)}
                  </time>
                  <button
                    className={styles.copyAction}
                    onClick={() => void handleCopy(message.id, content)}
                    type="button"
                  >
                    {getCopyLabel(message.id)}
                  </button>
                </div>
              </div>

              {content ? (
                message.role === "assistant" && !isStreamingAssistant ? (
                  <AssistantMarkdown content={content} />
                ) : (
                  <p className={styles.content}>{content}</p>
                )
              ) : null}

              {isStreamingAssistant ? (
                <div className={styles.streamFooter}>
                  <div className={styles.pendingRow}>
                    <span aria-hidden="true" className={styles.pendingPulse} />
                    <p className={styles.pendingText}>{content ? "继续生成中" : "正在生成回复"}</p>
                  </div>

                  {showStopAction && onStopGeneration ? (
                    <button className={styles.streamAction} onClick={() => onStopGeneration()} type="button">
                      {stopActionLabel}
                    </button>
                  ) : null}
                </div>
              ) : null}
            </article>
          </div>
        );
      })}
    </div>
  );
}
