"use client";

import { useState } from "react";

import type { AssistantSession } from "../../lib/assistant/types";
import styles from "./session-history.module.css";

type SessionHistoryProps = {
  activeSessionId: string | null;
  embedded?: boolean;
  isLoading?: boolean;
  onDeleteSession: (sessionId: string) => void;
  onNewConversation: () => void;
  onSelectSession: (sessionId: string) => void;
  sessions: AssistantSession[];
};

export function SessionHistory({
  activeSessionId,
  embedded = false,
  isLoading = false,
  onDeleteSession,
  onNewConversation,
  onSelectSession,
  sessions
}: SessionHistoryProps) {
  const [sessionToDelete, setSessionToDelete] = useState<string | null>(null);
  const sessionToDeleteTitle = sessions.find((s) => s.id === sessionToDelete)?.title ?? "";

  function handleDeleteConfirm() {
    if (!sessionToDelete) return;
    onDeleteSession(sessionToDelete);
    setSessionToDelete(null);
  }

  return (
    <section className={`${styles.sidebar} ${embedded ? styles.embedded : ""}`}>
      <div className={styles.top}>
        {embedded ? (
          <div className={styles.embeddedHeader}>
            <p className={styles.embeddedLabel}>最近会话</p>
          </div>
        ) : (
          <div className={styles.identity}>
            <p className={styles.label}>Assistant</p>
            <h1 className={styles.title}>个人助手</h1>
            <p className={styles.caption}>自由聊天、上传资料、把对话收敛成任务。</p>
          </div>
        )}

        <button className={styles.newButton} onClick={onNewConversation} type="button">
          新对话
        </button>
      </div>

      <div className={styles.listWrap}>
        {embedded ? null : <p className={styles.sectionLabel}>历史会话</p>}

        {isLoading ? (
          <p className={styles.placeholder}>正在加载历史</p>
        ) : sessions.length === 0 ? (
          <p className={styles.placeholder}>还没有历史会话</p>
        ) : (
          <ul className={styles.list}>
            {sessions.map((session) => (
              <li
                key={session.id}
                className={styles.item}
                data-active={session.id === activeSessionId}
              >
                <button
                  aria-label={`打开会话 ${session.title}`}
                  className={styles.sessionButton}
                  onClick={() => onSelectSession(session.id)}
                  type="button"
                >
                  <span className={styles.sessionTitle}>{session.title}</span>
                </button>

                <button
                  aria-label={`删除会话 ${session.title}`}
                  className={styles.deleteButton}
                  onClick={() => setSessionToDelete(session.id)}
                  type="button"
                >
                  ×
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {sessionToDelete ? (
        <div
          className={styles.modalBackdrop}
          onClick={() => setSessionToDelete(null)}
        >
          <div className={styles.modal} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle}>删除会话</h2>
            </div>
            <div className={styles.modalBody}>
              <p className={styles.modalText}>
                确定要删除会话「{sessionToDeleteTitle}」吗？此操作无法撤销。
              </p>
            </div>
            <div className={styles.modalFooter}>
              <button
                className={styles.modalCancelBtn}
                onClick={() => setSessionToDelete(null)}
                type="button"
              >
                取消
              </button>
              <button
                className={styles.modalConfirmBtn}
                onClick={handleDeleteConfirm}
                type="button"
              >
                删除
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  );
}
