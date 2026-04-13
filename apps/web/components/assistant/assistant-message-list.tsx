"use client";

import Link from "next/link";

import type { AssistantMessage } from "../../lib/assistant/types";
import { getFileDownloadURL } from "../../lib/api/files";
import styles from "./assistant-message-list.module.css";

type AssistantMessageListProps = {
  activeTaskSuggestionId: string | null;
  messages: AssistantMessage[];
  onConfirmTaskSuggestion: (messageId: string) => Promise<void> | void;
  pendingLabel?: string | null;
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
  pendingLabel = null
}: AssistantMessageListProps) {
  if (messages.length === 0 && pendingLabel === null) {
    return (
      <div className={styles.emptyState}>
        <p className={styles.emptyEyebrow}>新的对话</p>
        <h2 className={styles.emptyTitle}>发送第一条消息后才会真正创建会话。</h2>
        <p className={styles.emptyText}>你可以先自由聊，再决定是否创建任务。</p>
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
                <time className={styles.timestamp} dateTime={message.created_at}>{formatTime(message.created_at)}</time>
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
                <time className={styles.timestamp} dateTime={message.created_at}>{formatTime(message.created_at)}</time>
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
                <time className={styles.timestamp} dateTime={message.created_at}>{formatTime(message.created_at)}</time>
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

        return (
          <article key={message.id} className={styles.message} data-role={message.role}>
            <div className={styles.messageHeader}>
              <p className={styles.role}>{message.role === "assistant" ? "助手" : "你"}</p>
              <time className={styles.timestamp} dateTime={message.created_at}>{formatTime(message.created_at)}</time>
            </div>
            <p className={styles.content}>{message.payload.content}</p>
          </article>
        );
      })}

      {pendingLabel ? (
        <article aria-live="polite" className={`${styles.message} ${styles.pendingMessage}`} data-role="assistant">
          <p className={styles.role}>助手</p>
          <div className={styles.pendingRow}>
            <span aria-hidden="true" className={styles.pendingPulse} />
            <p className={styles.pendingText}>{pendingLabel}</p>
          </div>
        </article>
      ) : null}
    </div>
  );
}
