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
            resourceTitle: resourceMap.get(task.resource_id) || `资源 ${truncateId(task.resource_id, 8, 4)}`
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
              浏览资源
            </Link>
            <Link className={styles.actionButton} href="/approvals">
              打开审批中心
            </Link>
          </div>
        }
        label="工作台"
        title="工作台"
        description="集中查看最近任务流和两个关键入口，不做监控墙式信息堆砌。"
      >
        <p className={styles.banner}>输出 &gt; 已就绪，可发起审阅工作流</p>
      </TerminalFrame>

      <TerminalFrame
        label="最近任务"
        title="最近 5 条记录"
        description="最近 5 条任务按时间倒序输出，状态、文档标题和创建时间一并展示。"
      >
        {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}

        {isLoading ? (
          <p className={styles.placeholder}>正在加载任务流</p>
        ) : tasks.length === 0 ? (
          <p className={styles.placeholder}>暂无任务</p>
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
                    <span>创建时间 {toIsoSeconds(task.created_at)}</span>
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
