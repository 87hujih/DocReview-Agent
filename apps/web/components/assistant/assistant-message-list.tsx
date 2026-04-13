"use client";

import Link from "next/link";

import { getFileDownloadURL } from "../../lib/api/files";
import type { AssistantRenderableMessage } from "../../lib/assistant/types";
import styles from "./assistant-message-list.module.css";

type AssistantMessageListProps = {
  activeTaskSuggestionId: string | null;
  messages: AssistantRenderableMessage[];
  onConfirmTaskSuggestion: (messageId: string) => Promise<void> | void;
  onStopGeneration?: () => void;
  showStopAction?: boolean;
  stopActionLabel?: string;
};

function formatTime(isoString: string): string {
  const date = new Date(isoString);
  if (isNaN(date.getTime())) return "";
  const h = String(date.getHours()).padStart(2, "0");
  const m = String(date.getMinutes()).padStart(2, "0");
  return `${h}:${m}`;
}

export function AssistantMessageList({
  activeTaskSuggestionId,
  messages,
  onConfirmTaskSuggestion,
  onStopGeneration,
  showStopAction = false,
  stopActionLabel = "停止生成"
}: AssistantMessageListProps) {
  if (messages.length === 0) {
    return (
      <div className={styles.emptyState}>
        <p className={styles.emptyEyebrow}>个人助手</p>
        <h2 className={styles.emptyTitle}>有什么可以帮到你？</h2>
        <p className={styles.emptyText}>输入你的问题或需求，我来为你解答和整理。</p>
      </div>
    );
  }

  return (
    <div className={styles.list}>
      {messages.map((message) => {
        if (message.kind === "task_suggestion") {
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
                disabled={!message.payload.can_create || activeTaskSuggestionId === message.id}
                onClick={() => void onConfirmTaskSuggestion(message.id)}
                type="button"
              >
                {activeTaskSuggestionId === message.id ? "正在创建任务" : message.payload.action_label}
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
              <p className={styles.cardMeta}>任务状态：{message.payload.status}</p>
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
            <span className={styles.avatar} aria-hidden="true">
              {message.role === "assistant" ? (
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M12 2L14.09 8.26L20 9.27L15.55 13.97L16.91 20L12 16.9L7.09 20L8.45 13.97L4 9.27L9.91 8.26L12 2Z" />
                </svg>
              ) : (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M12 12c2.7 0 4.8-2.1 4.8-4.8S14.7 2.4 12 2.4 7.2 4.5 7.2 7.2 9.3 12 12 12zm0 2.4c-3.2 0-9.6 1.6-9.6 4.8v2.4h19.2v-2.4c0-3.2-6.4-4.8-9.6-4.8z" />
                </svg>
              )}
            </span>
            <article className={styles.message} data-role={message.role}>
              <div className={styles.messageHeader}>
                <p className={styles.role}>{message.role === "assistant" ? "助手" : "你"}</p>
                <time className={styles.timestamp} dateTime={message.created_at}>
                  {formatTime(message.created_at)}
                </time>
              </div>

              {content ? <p className={styles.content}>{content}</p> : null}

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
