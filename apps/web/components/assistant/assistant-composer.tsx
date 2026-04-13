"use client";

import { FormEvent, type KeyboardEvent, useId, useRef, useState } from "react";

import styles from "./assistant-composer.module.css";

const SUPPORTED_UPLOAD_ACCEPT = ".md,.txt,.doc,.docx,.pdf,.rtf,.odt";
const SUPPORTED_UPLOAD_HINT = "支持 md、txt、doc、docx、pdf、rtf、odt";

type AssistantComposerProps = {
  canUpload: boolean;
  isBusy?: boolean;
  onSubmitMessage: (message: string) => Promise<void> | void;
  onUploadFile: (file: File) => Promise<void> | void;
};

export function AssistantComposer({
  canUpload,
  isBusy = false,
  onSubmitMessage,
  onUploadFile
}: AssistantComposerProps) {
  const [input, setInput] = useState("");
  const [selectedFileName, setSelectedFileName] = useState<string | null>(null);
  const fileInputId = useId();
  const textareaRef = useRef<HTMLTextAreaElement>(null);

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
      <label className={styles.field} htmlFor="assistant-input">
        <span className={styles.label}>输入消息 · Enter 发送 · Ctrl+Enter 换行</span>
        <textarea
          ref={textareaRef}
          id="assistant-input"
          className={styles.textarea}
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="可以直接聊天，也可以让我整理成任务。"
          rows={4}
          value={input}
        />
      </label>

      <div className={styles.footer}>
        <div className={styles.uploadGroup}>
          <label className={styles.uploadButton} data-disabled={!canUpload || isBusy} htmlFor={fileInputId}>
            上传文件
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
          <span className={styles.uploadHint}>
            {canUpload
              ? selectedFileName
                ? `${selectedFileName} · ${SUPPORTED_UPLOAD_HINT}`
                : SUPPORTED_UPLOAD_HINT
              : `请先发送第一条消息后再上传 · ${SUPPORTED_UPLOAD_HINT}`}
          </span>
        </div>

        <button className={styles.submitButton} disabled={isBusy || input.trim() === ""} type="submit">
          {isBusy ? "处理中" : "发送"}
        </button>
      </div>
    </form>
  );
}
