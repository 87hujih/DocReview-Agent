"use client";

import type { CSSProperties } from "react";
import { startTransition, useEffect, useRef, useState } from "react";

import type { AssistantMessage, AssistantSession } from "../../lib/assistant/types";
import {
  appendAssistantMessage,
  confirmAssistantTaskSuggestion,
  createAssistantConversation,
  deleteAssistantSession,
  getAssistantSession,
  getAssistantSessions,
  uploadAssistantFile
} from "../../lib/api/assistant";
import { getErrorMessage } from "../../lib/terminal";
import { AssistantComposer } from "./assistant-composer";
import { AssistantMessageList } from "./assistant-message-list";
import { SessionHistory } from "./session-history";
import styles from "./assistant-shell.module.css";

const DEFAULT_SIDEBAR_WIDTH = 360;
const MAX_SIDEBAR_WIDTH = 520;
const MIN_SIDEBAR_WIDTH = 280;

type ShellStatus =
  | "confirming_task"
  | "creating_session"
  | "draft"
  | "load_failed"
  | "loading_conversation"
  | "loading_history"
  | "ready"
  | "sending_message"
  | "uploading_file";

export function AssistantShell() {
  const [sessions, setSessions] = useState<AssistantSession[]>([]);
  const [currentSession, setCurrentSession] = useState<AssistantSession | null>(null);
  const [messages, setMessages] = useState<AssistantMessage[]>([]);
  const [status, setStatus] = useState<ShellStatus>("loading_history");
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [sidebarWidth, setSidebarWidth] = useState(DEFAULT_SIDEBAR_WIDTH);
  const [activeTaskSuggestionId, setActiveTaskSuggestionId] = useState<string | null>(null);
  const [stickToBottom, setStickToBottom] = useState(true);
  const messageViewportRef = useRef<HTMLDivElement | null>(null);
  const resizeStateRef = useRef<{ startWidth: number; startX: number } | null>(null);

  useEffect(() => {
    let active = true;

    async function loadHistory() {
      try {
        const items = await getAssistantSessions();
        if (!active) {
          return;
        }

        startTransition(() => {
          setSessions(items);
          setStatus("draft");
          setHistoryError(null);
        });
      } catch (error) {
        if (!active) {
          return;
        }

        setHistoryError(getErrorMessage(error));
        setStatus("load_failed");
      }
    }

    void loadHistory();

    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    const viewport = messageViewportRef.current;
    if (!viewport) {
      return;
    }
    const node = viewport;

    function handleScroll() {
      const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
      setStickToBottom(distance < 80);
    }

    handleScroll();
    node.addEventListener("scroll", handleScroll);
    return () => {
      node.removeEventListener("scroll", handleScroll);
    };
  }, [currentSession?.id]);

  useEffect(() => {
    if (!stickToBottom) {
      return;
    }

    const viewport = messageViewportRef.current;
    if (!viewport) {
      return;
    }

    viewport.scrollTop = viewport.scrollHeight;
  }, [messages, stickToBottom, currentSession?.id]);

  useEffect(() => {
    function handleMouseMove(event: MouseEvent) {
      if (!resizeStateRef.current) {
        return;
      }

      const nextWidth = resizeStateRef.current.startWidth + (event.clientX - resizeStateRef.current.startX);
      setSidebarWidth(Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, nextWidth)));
    }

    function handleMouseUp() {
      resizeStateRef.current = null;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    }

    window.addEventListener("mousemove", handleMouseMove);
    window.addEventListener("mouseup", handleMouseUp);
    return () => {
      window.removeEventListener("mousemove", handleMouseMove);
      window.removeEventListener("mouseup", handleMouseUp);
    };
  }, []);

  function resetToDraft() {
    startTransition(() => {
      setCurrentSession(null);
      setMessages([]);
      setActionError(null);
      setStatus("draft");
      setStickToBottom(true);
    });
  }

  async function handleSelectSession(sessionId: string) {
    if (currentSession?.id === sessionId) {
      return;
    }

    setStatus("loading_conversation");
    setActionError(null);

    try {
      const result = await getAssistantSession(sessionId);
      startTransition(() => {
        setCurrentSession(result.session);
        setMessages(result.messages);
        setStatus("ready");
        setStickToBottom(true);
      });
    } catch (error) {
      setActionError(getErrorMessage(error));
      setStatus("ready");
    }
  }

  async function handleSubmitMessage(message: string) {
    setActionError(null);

    try {
      if (!currentSession) {
        setStatus("creating_session");
        const result = await createAssistantConversation(message);
        startTransition(() => {
          setCurrentSession(result.session);
          setMessages(result.messages);
          setSessions((current) => upsertSession(current, result.session));
          setStatus("ready");
          setStickToBottom(true);
        });
        return;
      }

      setStatus("sending_message");
      const result = await appendAssistantMessage(currentSession.id, message);
      startTransition(() => {
        setCurrentSession(result.session);
        setMessages((current) => [...current, ...result.messages]);
        setSessions((current) => upsertSession(current, result.session));
        setStatus("ready");
      });
    } catch (error) {
      setActionError(getErrorMessage(error));
      setStatus(currentSession ? "ready" : "draft");
    }
  }

  async function handleUploadFile(file: File) {
    if (!currentSession) {
      setActionError("请先发送第一条消息后再上传文件。");
      return;
    }

    setStatus("uploading_file");
    setActionError(null);

    try {
      const result = await uploadAssistantFile(currentSession.id, file);
      startTransition(() => {
        setCurrentSession(result.session);
        setMessages((current) => [...current, ...result.messages]);
        setSessions((current) => upsertSession(current, result.session));
        setActionError(result.error_message || null);
        setStatus("ready");
      });
    } catch (error) {
      setActionError(getErrorMessage(error));
      setStatus("ready");
    }
  }

  async function handleConfirmTaskSuggestion(messageId: string) {
    setActiveTaskSuggestionId(messageId);
    setStatus("confirming_task");
    setActionError(null);

    try {
      const result = await confirmAssistantTaskSuggestion(messageId);
      startTransition(() => {
        setCurrentSession(result.session);
        setMessages((current) => [...current, ...result.messages]);
        setSessions((current) => upsertSession(current, result.session));
        setActionError(result.error_message || null);
        setStatus("ready");
      });
    } catch (error) {
      setActionError(getErrorMessage(error));
      setStatus("ready");
    } finally {
      setActiveTaskSuggestionId(null);
    }
  }

  async function handleDeleteSession(sessionId: string) {
    try {
      await deleteAssistantSession(sessionId);
      setSessions((current) => current.filter((session) => session.id !== sessionId));
      if (currentSession?.id === sessionId) {
        resetToDraft();
      }
    } catch (error) {
      setActionError(getErrorMessage(error));
    }
  }

  const shellTitle = currentSession?.title || "开始新的对话";
  const isBusy =
    status === "creating_session" ||
    status === "sending_message" ||
    status === "uploading_file" ||
    status === "confirming_task" ||
    status === "loading_conversation";
  const pendingLabel = buildPendingLabel(status);

  return (
    <section
      className={styles.shell}
      style={
        {
          "--assistant-sidebar-width": `${sidebarWidth}px`
        } as CSSProperties
      }
    >
      <SessionHistory
        activeSessionId={currentSession?.id || null}
        isLoading={status === "loading_history"}
        onDeleteSession={(sessionId) => void handleDeleteSession(sessionId)}
        onNewConversation={resetToDraft}
        onSelectSession={(sessionId) => void handleSelectSession(sessionId)}
        sessions={sessions}
      />

      <div
        aria-orientation="vertical"
        className={styles.divider}
        onMouseDown={(event) => {
          resizeStateRef.current = {
            startWidth: sidebarWidth,
            startX: event.clientX
          };
          document.body.style.cursor = "col-resize";
          document.body.style.userSelect = "none";
          event.preventDefault();
        }}
        role="separator"
      />

      <section className={styles.workspace}>
        <header className={styles.workspaceHeader}>
          <div className={styles.headerText}>
            <p className={styles.workspaceLabel}>
              {currentSession ? "当前会话" : status === "loading_history" ? "正在启动" : "草稿态"}
            </p>
            <h2 className={styles.workspaceTitle}>{shellTitle}</h2>
          </div>

          {currentSession ? <span className={styles.headerState}>已持久化</span> : <span className={styles.headerState}>未创建</span>}
        </header>

        {historyError ? <p className={styles.banner}>历史加载失败：{historyError}</p> : null}
        {actionError ? <p className={styles.banner}>提示：{actionError}</p> : null}

        <div aria-busy={isBusy} className={styles.messageViewport} ref={messageViewportRef}>
          <AssistantMessageList
            activeTaskSuggestionId={activeTaskSuggestionId}
            messages={messages}
            onConfirmTaskSuggestion={(messageId) => void handleConfirmTaskSuggestion(messageId)}
            pendingLabel={pendingLabel}
          />
        </div>

        <AssistantComposer
          canUpload={currentSession !== null}
          isBusy={isBusy}
          onSubmitMessage={(message) => handleSubmitMessage(message)}
          onUploadFile={(file) => handleUploadFile(file)}
        />
      </section>
    </section>
  );
}

function buildPendingLabel(status: ShellStatus): string | null {
  switch (status) {
    case "creating_session":
    case "sending_message":
      return "助手处理中";
    case "uploading_file":
      return "正在上传资料";
    case "confirming_task":
      return "正在创建任务";
    default:
      return null;
  }
}

function upsertSession(current: AssistantSession[], session: AssistantSession): AssistantSession[] {
  const next = [session, ...current.filter((item) => item.id !== session.id)];
  return next.sort((left, right) => {
    if (left.last_message_at === right.last_message_at) {
      return left.id.localeCompare(right.id);
    }

    return right.last_message_at.localeCompare(left.last_message_at);
  });
}
