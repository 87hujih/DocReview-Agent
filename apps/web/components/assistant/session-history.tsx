"use client";

import type { AssistantSession } from "../../lib/assistant/types";
import styles from "./session-history.module.css";

type SessionHistoryProps = {
  activeSessionId: string | null;
  isLoading?: boolean;
  onDeleteSession: (sessionId: string) => void;
  onNewConversation: () => void;
  onSelectSession: (sessionId: string) => void;
  sessions: AssistantSession[];
};

export function SessionHistory({
  activeSessionId,
  isLoading = false,
  onDeleteSession,
  onNewConversation,
  onSelectSession,
  sessions
}: SessionHistoryProps) {
  return (
    <aside className={styles.sidebar}>
      <div className={styles.top}>
        <div className={styles.identity}>
          <p className={styles.label}>Assistant</p>
          <h1 className={styles.title}>个人助手</h1>
          <p className={styles.caption}>自由聊天、上传资料、把对话收敛成任务。</p>
        </div>

        <button className={styles.newButton} onClick={onNewConversation} type="button">
          新对话
        </button>
      </div>

      <div className={styles.listWrap}>
        <p className={styles.sectionLabel}>历史会话</p>

        {isLoading ? (
          <p className={styles.placeholder}>正在加载历史</p>
        ) : sessions.length === 0 ? (
          <p className={styles.placeholder}>还没有历史会话</p>
        ) : (
          <ul className={styles.list}>
            {sessions.map((session) => (
              <li key={session.id} className={styles.item}>
                <button
                  aria-label={`打开会话 ${session.title}`}
                  className={styles.sessionButton}
                  data-active={session.id === activeSessionId}
                  onClick={() => onSelectSession(session.id)}
                  type="button"
                >
                  <span className={styles.sessionTitle}>{session.title}</span>
                </button>

                <button
                  aria-label={`删除会话 ${session.title}`}
                  className={styles.deleteButton}
                  onClick={() => onDeleteSession(session.id)}
                  type="button"
                >
                  ×
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </aside>
  );
}
