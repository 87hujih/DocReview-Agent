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

type CopyActionIconProps = {
  state?: CopyState;
};

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

function CopyActionIcon({ state }: CopyActionIconProps) {
  if (state === "success") {
    return (
      <svg className={styles.copyIcon} data-copy-icon="copy-check" role="presentation" viewBox="0 0 24 24">
        <rect
          fill="none"
          height="14"
          rx="2"
          ry="2"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
          width="14"
          x="8"
          y="8"
        />
        <path
          d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"
          fill="none"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
        />
        <path
          d="m12 15 2 2 4-4"
          fill="none"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
        />
      </svg>
    );
  }

  if (state === "error") {
    return (
      <svg className={styles.copyIcon} role="presentation" viewBox="0 0 24 24">
        <path stroke="none" d="M0 0h24v24H0z" fill="none" />
        <path
          d="M12 9v4"
          fill="none"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
        />
        <path
          d="M10.363 3.591l-8.106 13.534a1.914 1.914 0 0 0 1.636 2.871h16.214a1.914 1.914 0 0 0 1.636 -2.87l-8.106 -13.536a1.914 1.914 0 0 0 -3.274 0"
          fill="none"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
        />
        <path d="M12 16h.01" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" />
      </svg>
    );
  }

  return (
    <svg className={styles.copyIcon} data-copy-icon="copy" role="presentation" viewBox="0 0 24 24">
      <rect
        fill="none"
        height="14"
        rx="2"
        ry="2"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="2"
        width="14"
        x="8"
        y="8"
      />
      <path
        d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"
        fill="none"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="2"
      />
    </svg>
  );
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

  function getCopyAriaLabel(messageId: string): string {
    const state = copyStates[messageId];
    if (state === "success") {
      return "复制成功";
    }

    if (state === "error") {
      return "复制失败";
    }

    return "复制消息";
  }

  function renderCopyAction(messageId: string, content: string) {
    const state = copyStates[messageId];

    return (
      <button
        aria-label={getCopyAriaLabel(messageId)}
        className={styles.copyAction}
        data-state={state ?? "idle"}
        onClick={() => void handleCopy(messageId, content)}
        type="button"
      >
        <CopyActionIcon state={state} />
      </button>
    );
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

        const messageBody = (
          <>
            <div className={styles.messageHeader}>
              <time className={styles.timestamp} dateTime={message.created_at}>
                {formatTime(message.created_at)}
              </time>
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
                  <span className={styles.pendingPulse} />
                  <p className={styles.pendingText}>{content ? "继续生成中" : "正在生成回复"}</p>
                </div>

                {showStopAction && onStopGeneration ? (
                  <button className={styles.streamAction} onClick={() => onStopGeneration()} type="button">
                    {stopActionLabel}
                  </button>
                ) : null}
              </div>
            ) : null}
          </>
        );

        if (message.role === "assistant") {
          return (
            <div key={message.id} className={styles.messageRow} data-role={message.role}>
              <article className={styles.message} data-role={message.role}>
                {messageBody}
                {content ? (
                  <div className={styles.assistantFooter} data-copy-anchor="assistant-footer">
                    {renderCopyAction(message.id, content)}
                  </div>
                ) : null}
              </article>
            </div>
          );
        }

        return (
          <div key={message.id} className={styles.messageRow} data-role={message.role}>
            <div className={styles.userMessageCluster}>
              {content ? (
                <div className={styles.userCopyRail} data-copy-anchor="user-rail">
                  {renderCopyAction(message.id, content)}
                </div>
              ) : null}
              <article className={styles.message} data-role={message.role}>
                {messageBody}
              </article>
            </div>
          </div>
        );
      })}
    </div>
  );
}
