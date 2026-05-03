"use client";

import type { MutableRefObject } from "react";
import { startTransition, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import type {
  AssistantMessage,
  AssistantRenderableMessage,
  AssistantSession,
  AssistantStreamEvent,
  AssistantTurnError
} from "../../lib/assistant/types";
import {
  confirmAssistantTaskSuggestion,
  deleteAssistantSession,
  getAssistantSession,
  getAssistantSessions,
  streamAssistantConversation,
  streamAssistantMessage,
  toAssistantTurnError,
  toggleWebSearch,
  uploadAssistantFile
} from "../../lib/api/assistant";
import { getErrorMessage } from "../../lib/terminal";
import { APP_RAIL_EXTRA_ID, useAppChrome } from "../app-chrome";
import { AssistantComposer } from "./assistant-composer";
import { AssistantMessageList } from "./assistant-message-list";
import { SessionHistory } from "./session-history";
import styles from "./assistant-shell.module.css";

type HistoryStatus = "history_error" | "history_ready" | "loading_history";
type TurnStatus = "confirming_task" | "idle" | "loading_conversation" | "stopping" | "streaming" | "uploading_file";

export function AssistantShell() {
  const { railCollapsed, setRailCollapsed } = useAppChrome();
  const [sessions, setSessions] = useState<AssistantSession[]>([]);
  const [currentSession, setCurrentSession] = useState<AssistantSession | null>(null);
  const [messages, setMessages] = useState<AssistantRenderableMessage[]>([]);
  const [historyStatus, setHistoryStatus] = useState<HistoryStatus>("loading_history");
  const [turnStatus, setTurnStatus] = useState<TurnStatus>("idle");
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [actionNotice, setActionNotice] = useState<string | null>(null);
  const [activeTaskSuggestionId, setActiveTaskSuggestionId] = useState<string | null>(null);
  const [stickToBottom, setStickToBottom] = useState(true);
  const [historyHost, setHistoryHost] = useState<HTMLElement | null>(null);
  const [webSearchEnabled, setWebSearchEnabled] = useState(false);
  const abortControllerRef = useRef<AbortController | null>(null);
  const localMessageCounterRef = useRef(0);
  const messageViewportRef = useRef<HTMLDivElement | null>(null);

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
          setHistoryError(null);
          setHistoryStatus("history_ready");
        });
      } catch (error) {
        if (!active) {
          return;
        }

        setHistoryError(getErrorMessage(error));
        setHistoryStatus("history_error");
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
    setHistoryHost(document.getElementById(APP_RAIL_EXTRA_ID));
    return () => {
      setHistoryHost(null);
    };
  }, [railCollapsed]);

  function resetToDraft() {
    abortControllerRef.current?.abort();
    startTransition(() => {
      setCurrentSession(null);
      setMessages([]);
      setActionNotice(null);
      setTurnStatus("idle");
      setStickToBottom(true);
      setWebSearchEnabled(false);
    });
  }

  async function handleSelectSession(sessionId: string) {
    if (turnStatus !== "idle" || currentSession?.id === sessionId) {
      return;
    }

    setActionNotice(null);
    setTurnStatus("loading_conversation");

    try {
      const result = await getAssistantSession(sessionId);
      setCurrentSession(result.session);
      setWebSearchEnabled(result.session.web_search_enabled);
      setMessages(result.messages);
      setStickToBottom(true);
      setTurnStatus("idle");
    } catch (error) {
      setActionNotice(getErrorMessage(error));
      setTurnStatus("idle");
    }
  }

  async function handleToggleWebSearch() {
    if (!currentSession || turnStatus !== "idle") {
      return;
    }

    const next = !webSearchEnabled;
    if (next) {
      const confirmed = window.confirm(
        "开启后，本会话可能把必要关键词发送到外部搜索服务。不会发送整份文档。你可以随时关闭。"
      );
      if (!confirmed) return;
    }

    try {
      const updated = await toggleWebSearch(currentSession.id, next);
      setWebSearchEnabled(updated.web_search_enabled);
      setCurrentSession(updated);
      setSessions((current) => upsertSession(current, updated));
    } catch (error) {
      setActionNotice(getErrorMessage(error));
    }
  }

  async function handleSubmitMessage(message: string) {
    if (turnStatus !== "idle") {
      return;
    }

    const localUserMessage = buildLocalTextMessage(
      nextLocalId(localMessageCounterRef, "local-user"),
      "user",
      message,
      messages.length + 1
    );
    const localAssistantMessage = buildLocalTextMessage(
      nextLocalId(localMessageCounterRef, "local-assistant"),
      "assistant",
      "",
      messages.length + 2,
      "streaming"
    );

    let sessionSnapshot = currentSession;
    const controller = new AbortController();
    abortControllerRef.current = controller;

    startTransition(() => {
      setActionNotice(null);
      setMessages((current) => [...removeLocalErrorMessages(current), localUserMessage, localAssistantMessage]);
      setStickToBottom(true);
      setTurnStatus("streaming");
    });

    const handleEvent = (event: AssistantStreamEvent) => {
      switch (event.type) {
        case "session_created":
          sessionSnapshot = event.session;
          startTransition(() => {
            setCurrentSession(event.session);
            setSessions((current) => upsertSession(current, event.session));
          });
          return;
        case "message_delta":
          startTransition(() => {
            setMessages((current) => updateLocalStreamingMessage(current, localAssistantMessage.id, event.delta));
          });
          return;
        case "message_completed":
          if (sessionSnapshot) {
            sessionSnapshot = patchSessionTimestamp(sessionSnapshot, event.message.created_at);
          }
          startTransition(() => {
            setMessages((current) => replaceLocalMessage(current, localAssistantMessage.id, event.message));
            if (sessionSnapshot) {
              setCurrentSession(sessionSnapshot);
              setSessions((current) => upsertSession(current, sessionSnapshot!));
            }
          });
          return;
        case "task_suggestion":
          if (sessionSnapshot) {
            sessionSnapshot = patchSessionTimestamp(sessionSnapshot, event.message.created_at);
          }
          startTransition(() => {
            setMessages((current) => [...current, event.message]);
            if (sessionSnapshot) {
              setCurrentSession(sessionSnapshot);
              setSessions((current) => upsertSession(current, sessionSnapshot!));
            }
          });
          return;
        default:
          return;
      }
    };

    try {
      const result = currentSession
        ? await streamAssistantMessage(currentSession.id, message, {
            onEvent: handleEvent,
            signal: controller.signal
          })
        : await streamAssistantConversation(message, {
            onEvent: handleEvent,
            signal: controller.signal
          });

      if (result.status === "stopped") {
        startTransition(() => {
          setMessages((current) => finalizeTurnFailure(current, localAssistantMessage.id, result.error));
          setTurnStatus("idle");
        });
        return;
      }

      startTransition(() => {
        setTurnStatus("idle");
      });
    } catch (error) {
      const turnError = toAssistantTurnError(error);
      startTransition(() => {
        setMessages((current) => finalizeTurnFailure(current, localAssistantMessage.id, turnError));
        setTurnStatus("idle");
      });
    } finally {
      if (abortControllerRef.current === controller) {
        abortControllerRef.current = null;
      }
    }
  }

  function handleStopGeneration() {
    if (turnStatus !== "streaming") {
      return;
    }

    setTurnStatus("stopping");
    abortControllerRef.current?.abort();
  }

  async function handleUploadFile(file: File) {
    if (!currentSession) {
      setActionNotice("请先发送第一条消息后再上传文件。");
      return;
    }
    if (turnStatus !== "idle") {
      return;
    }

    setActionNotice(null);
    setTurnStatus("uploading_file");

    try {
      const result = await uploadAssistantFile(currentSession.id, file);
      startTransition(() => {
        setCurrentSession(result.session);
        setMessages((current) => [...current, ...result.messages]);
        setSessions((current) => upsertSession(current, result.session));
        setActionNotice(result.error_message || null);
        setTurnStatus("idle");
      });
    } catch (error) {
      setActionNotice(getErrorMessage(error));
      setTurnStatus("idle");
    }
  }

  async function handleConfirmTaskSuggestion(messageId: string) {
    if (turnStatus !== "idle") {
      return;
    }

    setActionNotice(null);
    setActiveTaskSuggestionId(messageId);
    setTurnStatus("confirming_task");

    try {
      const result = await confirmAssistantTaskSuggestion(messageId);
      startTransition(() => {
        setCurrentSession(result.session);
        setMessages((current) => [...current, ...result.messages]);
        setSessions((current) => upsertSession(current, result.session));
        setActionNotice(result.error_message || null);
        setTurnStatus("idle");
      });
    } catch (error) {
      setActionNotice(getErrorMessage(error));
      setTurnStatus("idle");
    } finally {
      setActiveTaskSuggestionId(null);
    }
  }

  async function handleDeleteSession(sessionId: string) {
    if (turnStatus !== "idle") {
      return;
    }

    try {
      await deleteAssistantSession(sessionId);
      setSessions((current) => current.filter((session) => session.id !== sessionId));
      if (currentSession?.id === sessionId) {
        resetToDraft();
      }
    } catch (error) {
      setActionNotice(getErrorMessage(error));
    }
  }

  const isBusy = turnStatus !== "idle";
  const historyPanel = !railCollapsed ? (
    <SessionHistory
      activeSessionId={currentSession?.id || null}
      embedded
      isLoading={historyStatus === "loading_history"}
      onDeleteSession={(sessionId) => void handleDeleteSession(sessionId)}
      onNewConversation={resetToDraft}
      onSelectSession={(sessionId) => void handleSelectSession(sessionId)}
      sessions={sessions}
    />
  ) : null;

  return (
    <>
      {historyHost ? createPortal(historyPanel, historyHost) : null}
      <section className={styles.shell}>
        <section className={styles.workspace}>
          <div className={styles.workspaceTools}>
            <button
              aria-label={railCollapsed ? "展开侧边栏" : "收起侧边栏"}
              className={styles.sidebarToggle}
              onClick={() => setRailCollapsed((prev) => !prev)}
              title={railCollapsed ? "展开侧边栏" : "收起侧边栏"}
              type="button"
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                {railCollapsed ? (
                  <>
                    <rect x="3" y="4" width="18" height="16" rx="3" />
                    <polyline points="10 8 6 12 10 16" />
                  </>
                ) : (
                  <>
                    <rect x="3" y="4" width="18" height="16" rx="3" />
                    <polyline points="14 8 18 12 14 16" />
                  </>
                )}
              </svg>
            </button>
          </div>

          <div className={styles.banners}>
            {historyError ? <p className={styles.banner}>历史加载失败：{historyError}</p> : null}
            {actionNotice ? <p className={styles.banner}>提示：{actionNotice}</p> : null}
          </div>

          <div aria-busy={isBusy} className={styles.messageViewport} ref={messageViewportRef}>
            <AssistantMessageList
              activeTaskSuggestionId={activeTaskSuggestionId}
              messages={messages}
              onConfirmTaskSuggestion={(messageId) => void handleConfirmTaskSuggestion(messageId)}
              onStopGeneration={handleStopGeneration}
              showStopAction={turnStatus === "streaming" || turnStatus === "stopping"}
              stopActionLabel={turnStatus === "stopping" ? "正在停止" : "停止生成"}
            />
          </div>

          <AssistantComposer
            canUpload={currentSession !== null}
            isBusy={isBusy}
            onSubmitMessage={(nextMessage) => handleSubmitMessage(nextMessage)}
            onToggleWebSearch={currentSession ? () => void handleToggleWebSearch() : undefined}
            onUploadFile={(file) => handleUploadFile(file)}
            webSearchEnabled={webSearchEnabled}
          />
        </section>
      </section>
    </>
  );
}

function buildLocalTextMessage(
  id: string,
  role: "assistant" | "user",
  content: string,
  sequenceNo: number,
  localState?: "streaming"
) {
  return {
    created_at: new Date().toISOString(),
    id,
    kind: "local_text" as const,
    local_state: localState,
    payload: {
      content
    },
    role,
    sequence_no: sequenceNo
  };
}

function nextLocalId(counterRef: MutableRefObject<number>, prefix: string): string {
  counterRef.current += 1;
  return `${prefix}-${counterRef.current}`;
}

function patchSessionTimestamp(session: AssistantSession, isoString: string): AssistantSession {
  return {
    ...session,
    last_message_at: isoString,
    updated_at: isoString
  };
}

function replaceLocalMessage(
  current: AssistantRenderableMessage[],
  localMessageId: string,
  nextMessage: AssistantMessage
): AssistantRenderableMessage[] {
  return current.map((message) => (message.id === localMessageId ? nextMessage : message));
}

function updateLocalStreamingMessage(
  current: AssistantRenderableMessage[],
  localMessageId: string,
  delta: string
): AssistantRenderableMessage[] {
  return current.map((message) => {
    if (message.id !== localMessageId || message.kind !== "local_text") {
      return message;
    }

    return {
      ...message,
      payload: {
        content: message.payload.content + delta
      }
    };
  });
}

function removeLocalErrorMessages(current: AssistantRenderableMessage[]): AssistantRenderableMessage[] {
  return current.filter((message) => message.kind !== "local_error");
}

function finalizeTurnFailure(
  current: AssistantRenderableMessage[],
  localMessageId: string,
  turnError: AssistantTurnError
): AssistantRenderableMessage[] {
  const nextMessages: AssistantRenderableMessage[] = [];
  let sequenceNo = 0;

  for (const message of current) {
    sequenceNo = Math.max(sequenceNo, message.sequence_no);

    if (message.id !== localMessageId || message.kind !== "local_text") {
      nextMessages.push(message);
      continue;
    }

    if (message.payload.content.trim() === "") {
      continue;
    }

    nextMessages.push({
      ...message,
      local_state: undefined
    });
  }

  nextMessages.push({
    created_at: new Date().toISOString(),
    id: `local-error-${sequenceNo + 1}`,
    kind: "local_error",
    payload: {
      code: turnError.code,
      content: turnError.message
    },
    role: "assistant",
    sequence_no: sequenceNo + 1
  });

  return nextMessages;
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
