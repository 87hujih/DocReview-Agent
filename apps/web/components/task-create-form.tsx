"use client";

import { FormEvent, useState } from "react";

import { toIsoSeconds } from "../lib/terminal";
import { MetaRow } from "./ui/meta-row";
import { TerminalFrame } from "./ui/terminal-frame";
import styles from "./task-create-form.module.css";

export type TaskCreateFormResource = {
  createdAt: string;
  id: string;
  sourceType: string;
  title: string;
};

type TaskCreateFormProps = {
  errorMessage?: string | null;
  isSubmitting?: boolean;
  onSubmit: (instruction: string) => Promise<void> | void;
  resource: TaskCreateFormResource | null;
};

export function TaskCreateForm({
  errorMessage,
  isSubmitting = false,
  onSubmit,
  resource
}: TaskCreateFormProps) {
  const [instruction, setInstruction] = useState("");

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const trimmedInstruction = instruction.trim();
    if (!trimmedInstruction || !resource) {
      return;
    }

    await onSubmit(trimmedInstruction);
  }

  return (
    <TerminalFrame
      label="创建任务"
      title="提交任务"
    >
      <form className={styles.form} onSubmit={handleSubmit}>
        <div className={styles.resourcePanel}>
          <MetaRow label="资源 ID" value={resource ? resource.id : "未选择"} />
          <MetaRow label="资源标题" value={resource ? resource.title : "请选择资源"} />
          <MetaRow label="来源类型" value={resource ? resource.sourceType : "未知"} />
          <MetaRow
            highlight
            label="创建时间"
            value={resource ? toIsoSeconds(resource.createdAt) : "未提供"}
          />
        </div>

        <label className={styles.field} htmlFor="task-instruction">
          <span className={styles.label}>修订指令</span>
          <textarea
            id="task-instruction"
            className={styles.textarea}
            name="instruction"
            onChange={(event) => setInstruction(event.target.value)}
            placeholder="请描述您希望对该文档进行的修订或优化..."
            rows={8}
            value={instruction}
          />
        </label>

        {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}

        <button className={styles.button} disabled={!resource || isSubmitting} type="submit">
          {isSubmitting ? "提交中" : "提交任务"}
        </button>
      </form>
    </TerminalFrame>
  );
}
