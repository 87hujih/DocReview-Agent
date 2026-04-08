"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

import { TerminalFrame } from "../components/ui/terminal-frame";
import { StatusChip } from "../components/ui/status-chip";
import { getResources } from "../lib/api/resources";
import { getTasks, type Task } from "../lib/api/tasks";
import { getErrorMessage, toIsoSeconds, truncateId } from "../lib/terminal";
import styles from "./page.module.css";

type DashboardTask = Task & {
  resourceTitle: string;
};

export default function HomePage() {
  const [tasks, setTasks] = useState<DashboardTask[]>([]);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    let active = true;

    async function loadDashboard() {
      try {
        const [taskItems, resources] = await Promise.all([getTasks(), getResources()]);
        if (!active) {
          return;
        }

        const resourceMap = new Map(resources.map((resource) => [resource.id, resource.title]));
        setTasks(
          taskItems.slice(0, 5).map((task) => ({
            ...task,
            resourceTitle: resourceMap.get(task.resource_id) || `RESOURCE ${truncateId(task.resource_id, 8, 4)}`
          }))
        );
        setErrorMessage(null);
      } catch (error) {
        if (active) {
          setErrorMessage(getErrorMessage(error));
        }
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    }

    void loadDashboard();

    return () => {
      active = false;
    };
  }, []);

  return (
    <div className={styles.page}>
      <TerminalFrame
        actions={
          <div className={styles.actions}>
            <Link className={styles.actionButton} href="/resources">
              BROWSE_RESOURCES
            </Link>
            <Link className={styles.actionButton} href="/approvals">
              OPEN_APPROVALS
            </Link>
          </div>
        }
        label="WORKBENCH"
        title="工作台"
        description="集中查看最近任务流和两个关键入口，不做监控墙式信息堆砌。"
      >
        <p className={styles.banner}>STDOUT &gt; READY_FOR_AGENT_WORKFLOWS</p>
      </TerminalFrame>

      <TerminalFrame
        label="RECENT_TASKS"
        title="LAST_5_ENTRIES"
        description="最近 5 条任务按时间倒序输出，状态、文档标题和创建时间一并展示。"
      >
        {errorMessage ? <p className={styles.error}>STDERR &gt; {errorMessage}</p> : null}

        {isLoading ? (
          <p className={styles.placeholder}>LOADING_TASK_STREAM</p>
        ) : tasks.length === 0 ? (
          <p className={styles.placeholder}>NO_TASKS_FOUND</p>
        ) : (
          <ul className={styles.taskList}>
            {tasks.map((task) => (
              <li key={task.id}>
                <Link className={styles.taskItem} href={`/tasks/${task.id}`}>
                  <div className={styles.taskHeader}>
                    <StatusChip status={task.status} />
                    <span className={styles.taskTitle}>{task.resourceTitle}</span>
                  </div>

                  <div className={styles.taskMeta}>
                    <span>ID: {truncateId(task.id, 8, 4)}</span>
                    <span>CREATED_AT {toIsoSeconds(task.created_at)}</span>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </TerminalFrame>
    </div>
  );
}
