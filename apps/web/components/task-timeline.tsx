import type { TaskEvent, TaskStep } from "../lib/api/tasks";
import { formatDuration, formatStepLabel, formatStatusLabel, toIsoSeconds } from "../lib/terminal";
import { LogLine } from "./ui/log-line";
import { MetaRow } from "./ui/meta-row";
import { StatusChip } from "./ui/status-chip";
import { TerminalFrame } from "./ui/terminal-frame";
import styles from "./task-timeline.module.css";

type TaskTimelineProps = {
  events?: TaskEvent[];
  steps: TaskStep[];
};

export function TaskTimeline({ events = [], steps }: TaskTimelineProps) {
  return (
    <TerminalFrame
      label="执行流"
      title="任务时间线"
    >
      {steps.length === 0 ? (
        <LogLine prefix="输出 >" tone="warning">
          正在等待步骤事件
        </LogLine>
      ) : (
        <ol className={styles.list}>
          {steps.map((step) => {
            const duration = formatDuration(step.started_at, step.completed_at);

            return (
              <li key={step.id} className={styles.item}>
                <div className={styles.header}>
                  <span className={styles.stepName}>{formatStepLabel(step.step_name)}</span>
                  <StatusChip status={step.status} />
                </div>

                <div className={styles.meta}>
                  <MetaRow label="开始时间" value={toIsoSeconds(step.started_at)} />
                  <MetaRow label="完成时间" value={toIsoSeconds(step.completed_at)} />
                  <MetaRow label="耗时" value={duration || "执行中"} />
                </div>

                <LogLine prefix="输出 >" tone="info">
                  步骤状态 {formatStatusLabel(step.status)}
                </LogLine>

                {step.error_message ? (
                  <LogLine prefix="错误 >" tone="error">
                    {step.error_message}
                  </LogLine>
                ) : null}
              </li>
            );
          })}
        </ol>
      )}

      {events.length > 0 ? (
        <section className={styles.events} aria-label="任务事件流">
          <h3 className={styles.eventsTitle}>事件流</h3>
          <ol className={styles.eventList}>
            {events.map((event) => (
              <li key={event.id} className={styles.eventItem} data-level={event.level}>
                <div className={styles.eventHeader}>
                  <span className={styles.eventType}>{event.event_type}</span>
                  <span className={styles.eventTime}>{toIsoSeconds(event.created_at)}</span>
                </div>
                <LogLine prefix={`${event.source} >`} tone={event.level === "error" ? "error" : "info"}>
                  {event.message}
                </LogLine>
                {event.step_name ? <MetaRow label="step" value={formatStepLabel(event.step_name)} /> : null}
              </li>
            ))}
          </ol>
        </section>
      ) : null}
    </TerminalFrame>
  );
}
