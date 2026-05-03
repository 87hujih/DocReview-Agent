"use client";

import { FormEvent, type KeyboardEvent, useEffect, useId, useRef, useState } from "react";

import styles from "./assistant-composer.module.css";

const SUPPORTED_UPLOAD_ACCEPT = ".md,.txt,.doc,.docx,.pdf,.rtf,.odt";
const SUPPORTED_UPLOAD_HINT = "支持 md、txt、doc、docx、pdf、rtf、odt";
const MAX_TEXTAREA_HEIGHT = 250;
const MIN_TEXTAREA_HEIGHT = 56;

type AssistantComposerProps = {
  canUpload: boolean;
  isBusy?: boolean;
  onSubmitMessage: (message: string) => Promise<void> | void;
  onToggleWebSearch?: () => void;
  onUploadFile: (file: File) => Promise<void> | void;
  webSearchEnabled?: boolean;
};

export function AssistantComposer({
  canUpload,
  isBusy = false,
  onSubmitMessage,
  onToggleWebSearch,
  onUploadFile,
  webSearchEnabled = false
}: AssistantComposerProps) {
  const [input, setInput] = useState("");
  const [selectedFileName, setSelectedFileName] = useState<string | null>(null);
  const fileInputId = useId();
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    const nextHeight = Math.min(Math.max(el.scrollHeight, MIN_TEXTAREA_HEIGHT), MAX_TEXTAREA_HEIGHT);
    el.style.height = `${nextHeight}px`;
    el.style.overflowY = el.scrollHeight > MAX_TEXTAREA_HEIGHT ? "auto" : "hidden";
  }, [input]);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const trimmed = input.trim();
    if (!trimmed || isBusy) {
      return;
    }

    await onSubmitMessage(trimmed);
    setInput("");
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== "Enter") return;

    if (event.ctrlKey || event.metaKey) {
      // Ctrl+Enter / Cmd+Enter：在光标位置插入换行
      event.preventDefault();
      const el = event.currentTarget;
      const start = el.selectionStart ?? 0;
      const end = el.selectionEnd ?? 0;
      const next = input.slice(0, start) + "\n" + input.slice(end);
      const nextCursor = start + 1;
      setInput(next);
      // 受控组件 setState 是异步的，需在 DOM 更新后恢复光标
      requestAnimationFrame(() => {
        if (textareaRef.current) {
          textareaRef.current.selectionStart = nextCursor;
          textareaRef.current.selectionEnd = nextCursor;
        }
      });
    } else {
      // Enter：发送消息
      event.preventDefault();
      const trimmed = input.trim();
      if (!trimmed || isBusy) return;
      void onSubmitMessage(trimmed);
      setInput("");
    }
  }

  return (
    <form className={styles.form} onSubmit={(event) => void handleSubmit(event)}>
      <div className={styles.inputWrap}>
        <textarea
          aria-label="输入消息"
          ref={textareaRef}
          id="assistant-input"
          className={styles.textarea}
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="可以直接聊天，也可以让我整理成任务。"
          rows={1}
          value={input}
        />

        <div className={styles.inputActions}>
          <label
            className={styles.uploadBtn}
            data-disabled={!canUpload || isBusy}
            htmlFor={fileInputId}
            title="上传文件"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48" />
            </svg>
          </label>
          <input
            accept={SUPPORTED_UPLOAD_ACCEPT}
            className={styles.fileInput}
            disabled={!canUpload || isBusy}
            id={fileInputId}
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (!file) {
                return;
              }

              setSelectedFileName(file.name);
              void onUploadFile(file);
              event.target.value = "";
            }}
            type="file"
          />
          <span className={styles.fileName}>
            {canUpload
              ? selectedFileName
                ? `${selectedFileName} · ${SUPPORTED_UPLOAD_HINT}`
                : SUPPORTED_UPLOAD_HINT
              : `请先发送第一条消息后再上传 · ${SUPPORTED_UPLOAD_HINT}`}
          </span>
          {onToggleWebSearch ? (
            <button
              aria-label={webSearchEnabled ? "关闭联网搜索" : "开启联网搜索"}
              className={styles.webSearchBtn}
              data-active={webSearchEnabled}
              disabled={isBusy}
              onClick={onToggleWebSearch}
              title={webSearchEnabled ? "联网搜索已开启，点击关闭" : "开启联网搜索"}
              type="button"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10" />
                <line x1="2" y1="12" x2="22" y2="12" />
                <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
              </svg>
              <span>{webSearchEnabled ? "联网中" : "联网"}</span>
            </button>
          ) : null}
          <button
            aria-label="发送"
            className={styles.sendBtn}
            disabled={isBusy || input.trim() === ""}
            title="发送"
            type="submit"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
              <path d="M2.01 21L23 12 2.01 3 2 10l15 2-15 2z" />
            </svg>
          </button>
        </div>
      </div>
    </form>
  );
}
