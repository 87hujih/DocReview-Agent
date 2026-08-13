"use client";

import type { MutableRefObject } from "react";
import { startTransition, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import type {
  AssistantCapabilities,
  AssistantMessage,
  AssistantRenderableMessage,
  AssistantSession,
  AssistantStreamEvent,
  AssistantTurnError,
  AssistantUploadCapabilities
} from "../../lib/assistant/types";
import {
  createAssistantConversationWithFile,
  deleteAssistantSession,
  getAssistantCapabilities,
  getAssistantSession,
  getAssistantSessions,
  streamAssistantConversation,
  streamAssistantMessage,
  toAssistantTurnError,
  uploadAssistantFile
} from "../../lib/api/assistant";
import { getErrorMessage } from "../../lib/terminal";
import { APP_RAIL_EXTRA_ID, useAppChrome } from "../app-chrome";
import { AssistantComposer } from "./assistant-composer";
import { AssistantMessageList } from "./assistant-message-list";
import { SessionHistory } from "./session-history";
import styles from "./assistant-shell.module.css";

type HistoryStatus = "history_error" | "history_ready" | "loading_history";
type TurnStatus = "idle" | "loading_conversation" | "stopping" | "streaming" | "uploading_file";

type AssistantShellProps = {
  initialResourceId?: string | null;
  initialSessionId?: string | null;
};

const FALLBACK_UPLOAD_CAPABILITIES: AssistantUploadCapabilities = {
  accept: ".md,.txt",
  hint: "支持 md、txt",
  supported_extensions: [".md", ".txt"]
};

export function AssistantShell({ initialResourceId = null, initialSessionId = null }: AssistantShellProps) {
  const { railCollapsed, setRailCollapsed } = useAppChrome();
  const [sessions, setSessions] = useState<AssistantSession[]>([]);
  const [currentSession, setCurrentSession] = useState<AssistantSession | null>(null);
  const [messages, setMessages] = useState<AssistantRenderableMessage[]>([]);
  const [historyStatus, setHistoryStatus] = useState<HistoryStatus>("loading_history");
  const [turnStatus, setTurnStatus] = useState<TurnStatus>("idle");
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [actionNotice, setActionNotice] = useState<string | null>(null);
  const [stickToBottom, setStickToBottom] = useState(true);
  const [historyHost, setHistoryHost] = useState<HTMLElement | null>(null);
  const [uploadCapabilities, setUploadCapabilities] = useState<AssistantUploadCapabilities>(FALLBACK_UPLOAD_CAPABILITIES);
  const abortControllerRef = useRef<AbortController | null>(null);
  const initialResourceIdRef = useRef(normalizeSessionId(initialResourceId));
  const initialSessionIdRef = useRef(normalizeSessionId(initialSessionId));
  const localMessageCounterRef = useRef(0);
  const messageViewportRef = useRef<HTMLDivElement | null>(null);
  const turnTokenRef = useRef(0);

  useEffect(() => {
    let active = true;

    async function loadInitialData() {
      const [historyResult, capabilitiesResult] = await Promise.allSettled([
        getAssistantSessions(),
        getAssistantCapabilities()
      ]);
      if (!active) {
        return;
      }

      const loadedSessions = historyResult.status === "fulfilled" ? historyResult.value : [];

      startTransition(() => {
        if (historyResult.status === "fulfilled") {
          setSessions(loadedSessions);
          setHistoryError(null);
          setHistoryStatus("history_ready");
        } else {
          setHistoryError(getErrorMessage(historyResult.reason));
          setHistoryStatus("history_error");
        }

        if (capabilitiesResult.status === "fulfilled") {
          setUploadCapabilities(normalizeAssistantUploadCapabilities(capabilitiesResult.value));
        } else {
          setUploadCapabilities(FALLBACK_UPLOAD_CAPABILITIES);
        }
      });

      const requestedSessionId = initialSessionIdRef.current;
      initialSessionIdRef.current = null;
      if (requestedSessionId === null) {
        return;
      }

      startTransition(() => {
        setActionNotice(null);
        setTurnStatus("loading_conversation");
      });

      try {
        const result = await getAssistantSession(requestedSessionId);
        if (!active) {
          return;
        }

        replaceSessionQuery(result.session.id);
        startTransition(() => {
          setCurrentSession(result.session);
          setMessages(result.messages);
          setSessions((current) => upsertSession(current.length > 0 ? current : loadedSessions, result.session));
          setStickToBottom(true);
          setTurnStatus("idle");
        });
      } catch {
        if (!active) {
          return;
        }

        replaceSessionQuery(null);
        startTransition(() => {
          setCurrentSession(null);
          setMessages([]);
          setStickToBottom(true);
          setTurnStatus("idle");
        });
      }
    }

    void loadInitialData();

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
    invalidateCurrentTurn(turnTokenRef);
    abortControllerRef.current?.abort();
    abortControllerRef.current = null;
    replaceSessionQuery(null);
    startTransition(() => {
      setCurrentSession(null);
      setMessages([]);
      setActionNotice(null);
      setTurnStatus("idle");
      setStickToBottom(true);
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
      replaceSessionQuery(result.session.id);
      setCurrentSession(result.session);
      setMessages(result.messages);
      setStickToBottom(true);
      setTurnStatus("idle");
    } catch (error) {
      setActionNotice(getErrorMessage(error));
      setTurnStatus("idle");
    }
  }

  async function handleSubmitMessage(message: string) {
    if (turnStatus !== "idle") {
      return;
    }

    // 将本轮请求绑定到最近一次已就绪的持久化资源，避免会话与文档作用域脱节。
    const resourceId = latestReadySessionResource(messages) ?? initialResourceIdRef.current ?? undefined;

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
    const turnToken = beginTurn(turnTokenRef);
    const controller = new AbortController();
    abortControllerRef.current = controller;

    startTransition(() => {
      setActionNotice(null);
      setMessages((current) => [...removeLocalErrorMessages(current), localUserMessage, localAssistantMessage]);
      setStickToBottom(true);
      setTurnStatus("streaming");
    });

    const handleEvent = (event: AssistantStreamEvent) => {
      if (!isActiveTurn(turnTokenRef, turnToken)) {
        return;
      }

      switch (event.type) {
        case "session_created":
          sessionSnapshot = event.session;
          replaceSessionQuery(event.session.id);
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
        case "session_file":
          if (sessionSnapshot) {
            sessionSnapshot = patchSessionTimestamp(sessionSnapshot, event.message.created_at);
          }
          startTransition(() => {
            setMessages((current) =>
              insertMessageBeforeStreamingPlaceholder(current, localAssistantMessage.id, event.message)
            );
            if (sessionSnapshot) {
              setCurrentSession(sessionSnapshot);
              setSessions((current) => upsertSession(current, sessionSnapshot!));
            }
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
        case "turn_state":
          if (event.status === "waiting_approval" || event.status === "waiting_input") {
            const notice = event.status === "waiting_approval"
              ? "此轮正在等待外部审批；审批完成后会从持久化状态自动继续。"
              : "此轮正在等待补充输入；当前状态已持久化，可安全恢复。";
            startTransition(() => {
              setActionNotice(notice);
              setMessages((current) => finalizeTurnWait(current, localAssistantMessage.id, notice));
            });
          }
          return;
        default:
          return;
      }
    };

    try {
      const result = currentSession
        ? await streamAssistantMessage(currentSession.id, message, {
            onEvent: handleEvent,
            resourceId,
            signal: controller.signal
          })
        : await streamAssistantConversation(message, {
            onEvent: handleEvent,
            resourceId,
            signal: controller.signal
          });

      if (!isActiveTurn(turnTokenRef, turnToken)) {
        return;
      }

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
      if (!isActiveTurn(turnTokenRef, turnToken)) {
        return;
      }

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
    if (turnStatus !== "idle") {
      return;
    }

    setActionNotice(null);
    setTurnStatus("uploading_file");

    try {
      const result = currentSession
        ? await uploadAssistantFile(currentSession.id, file)
        : await createAssistantConversationWithFile(file);
      replaceSessionQuery(result.session.id);
      startTransition(() => {
        setCurrentSession(result.session);
        setMessages((current) => [...current, ...result.messages]);
        setSessions((current) => upsertSession(current, result.session));
        setActionNotice(result.error_message || null);
        setStickToBottom(true);
        setTurnStatus("idle");
      });
    } catch (error) {
      setActionNotice(getErrorMessage(error));
      setTurnStatus("idle");
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
              messages={messages}
              onStopGeneration={handleStopGeneration}
              showStopAction={turnStatus === "streaming" || turnStatus === "stopping"}
              stopActionLabel={turnStatus === "stopping" ? "正在停止" : "停止生成"}
            />
          </div>

          <AssistantComposer
            canUpload
            isBusy={isBusy}
            onSubmitMessage={(nextMessage) => handleSubmitMessage(nextMessage)}
            onUploadFile={(file) => handleUploadFile(file)}
            uploadCapabilities={uploadCapabilities}
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

function beginTurn(turnTokenRef: MutableRefObject<number>): number {
  turnTokenRef.current += 1;
  return turnTokenRef.current;
}

function invalidateCurrentTurn(turnTokenRef: MutableRefObject<number>) {
  turnTokenRef.current += 1;
}

function isActiveTurn(turnTokenRef: MutableRefObject<number>, turnToken: number): boolean {
  return turnTokenRef.current === turnToken;
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

function insertMessageBeforeStreamingPlaceholder(
  current: AssistantRenderableMessage[],
  localMessageId: string,
  nextMessage: AssistantMessage
): AssistantRenderableMessage[] {
  const placeholderIndex = current.findIndex((message) => message.id === localMessageId);
  if (placeholderIndex === -1) {
    return [...current, nextMessage];
  }

  return [
    ...current.slice(0, placeholderIndex),
    nextMessage,
    ...current.slice(placeholderIndex)
  ];
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

// latestReadySessionResource 从后向前查找当前会话最近一次已就绪的资源。
function latestReadySessionResource(messages: AssistantRenderableMessage[]): string | undefined {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (message.kind !== "session_file" || message.payload.status !== "ready") {
      continue;
    }
    const resourceId = message.payload.resource_id.trim();
    if (resourceId !== "") {
      return resourceId;
    }
  }
  return undefined;
}

// finalizeTurnWait 将本地占位消息更新为可恢复的等待状态说明。
function finalizeTurnWait(
  current: AssistantRenderableMessage[],
  localMessageId: string,
  content: string
): AssistantRenderableMessage[] {
  return current.map((message) => {
    if (message.id !== localMessageId || message.kind !== "local_text") {
      return message;
    }
    return {
      ...message,
      local_state: undefined,
      payload: { content }
    };
  });
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

function normalizeAssistantUploadCapabilities(capabilities: AssistantCapabilities): AssistantUploadCapabilities {
  const rawUpload = capabilities.upload;
  const supportedExtensions =
    rawUpload && Array.isArray(rawUpload.supported_extensions)
      ? [...rawUpload.supported_extensions]
      : [...FALLBACK_UPLOAD_CAPABILITIES.supported_extensions];
  const accept =
    rawUpload && typeof rawUpload.accept === "string" && rawUpload.accept.trim() !== ""
      ? rawUpload.accept
      : supportedExtensions.join(",");
  const hint =
    rawUpload && typeof rawUpload.hint === "string" && rawUpload.hint.trim() !== ""
      ? rawUpload.hint
      : buildUploadHintFromExtensions(supportedExtensions);

  return {
    accept,
    hint,
    supported_extensions: supportedExtensions
  };
}

function buildUploadHintFromExtensions(supportedExtensions: string[]): string {
  if (supportedExtensions.length === 0) {
    return "当前服务未开放文件上传";
  }

  return `支持 ${supportedExtensions.map((extension) => extension.replace(/^\./, "")).join("、")}`;
}

function normalizeSessionId(value: string | null | undefined): string | null {
  if (typeof value !== "string") {
    return null;
  }

  const trimmed = value.trim();
  return trimmed === "" ? null : trimmed;
}

function replaceSessionQuery(sessionId: string | null) {
  if (typeof window === "undefined") {
    return;
  }

  const url = new URL(window.location.href);
  if (sessionId) {
    url.searchParams.set("session", sessionId);
  } else {
    url.searchParams.delete("session");
  }

  const nextURL = `${url.pathname}${url.search}${url.hash}`;
  window.history.replaceState(window.history.state, "", nextURL || "/");
}
