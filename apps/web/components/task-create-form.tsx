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
      label="TASK_CREATE"
      title="SUBMIT_TASK"
      description="提交一条结构化修订指令，触发 planner -> retriever -> reviewer -> editor 流程。"
    >
      <form className={styles.form} onSubmit={handleSubmit}>
        <div className={styles.resourcePanel}>
          <MetaRow label="resource_id" value={resource ? resource.id : "NOT_SELECTED"} />
          <MetaRow label="resource_title" value={resource ? resource.title : "SELECT_RESOURCE"} />
          <MetaRow label="source_type" value={resource ? resource.sourceType : "UNKNOWN"} />
          <MetaRow
            highlight
            label="created_at"
            value={resource ? toIsoSeconds(resource.createdAt) : "N/A"}
          />
        </div>

        <label className={styles.field} htmlFor="task-instruction">
          <span className={styles.label}>INSTRUCTION</span>
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

        {errorMessage ? <p className={styles.error}>STDERR &gt; {errorMessage}</p> : null}

        <button className={styles.button} disabled={!resource || isSubmitting} type="submit">
          {isSubmitting ? "SUBMITTING" : "SUBMIT_TASK"}
        </button>
      </form>
    </TerminalFrame>
  );
}
