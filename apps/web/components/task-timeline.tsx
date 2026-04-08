import type { TaskStep } from "../lib/api/tasks";
import { formatDuration, formatToken, toIsoSeconds } from "../lib/terminal";
import { LogLine } from "./ui/log-line";
import { MetaRow } from "./ui/meta-row";
import { StatusChip } from "./ui/status-chip";
import { TerminalFrame } from "./ui/terminal-frame";
import styles from "./task-timeline.module.css";

type TaskTimelineProps = {
  steps: TaskStep[];
};

export function TaskTimeline({ steps }: TaskTimelineProps) {
  return (
    <TerminalFrame
      label="EXECUTION_FLOW"
      title="TASK_TIMELINE"
      description="真实步骤流按创建顺序输出，状态与时间信息全部以终端日志格式展示。"
    >
      {steps.length === 0 ? (
        <LogLine prefix="STDOUT >" tone="warning">
          WAITING_FOR_STEP_EVENTS
        </LogLine>
      ) : (
        <ol className={styles.list}>
          {steps.map((step) => {
            const duration = formatDuration(step.started_at, step.completed_at);

            return (
              <li key={step.id} className={styles.item}>
                <div className={styles.header}>
                  <span className={styles.stepName}>{formatToken(step.step_name)}</span>
                  <StatusChip status={step.status} />
                </div>

                <div className={styles.meta}>
                  <MetaRow label="started_at" value={toIsoSeconds(step.started_at)} />
                  <MetaRow label="completed_at" value={toIsoSeconds(step.completed_at)} />
                  <MetaRow label="runtime" value={duration || "IN_PROGRESS"} />
                </div>

                <LogLine prefix="STDOUT >" tone="info">
                  STEP_STATUS {formatToken(step.status)}
                </LogLine>

                {step.error_message ? (
                  <LogLine prefix="STDERR >" tone="error">
                    {step.error_message}
                  </LogLine>
                ) : null}
              </li>
            );
          })}
        </ol>
      )}
    </TerminalFrame>
  );
}
