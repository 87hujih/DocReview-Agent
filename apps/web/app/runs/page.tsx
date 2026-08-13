"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";

import { MetaRow } from "../../components/ui/meta-row";
import { StatusChip } from "../../components/ui/status-chip";
import { TerminalFrame } from "../../components/ui/terminal-frame";
import { getRuns, type RunSummary } from "../../lib/api/runs";
import { getErrorMessage, toIsoSeconds, truncateId } from "../../lib/terminal";
import styles from "./page.module.css";

const RUN_STATUSES = [
  { label: "全部", value: "" },
  { label: "排队中", value: "queued" },
  { label: "运行中", value: "running" },
  { label: "待输入", value: "waiting_input" },
  { label: "待审批", value: "waiting_approval" },
  { label: "已完成", value: "succeeded" },
  { label: "失败", value: "failed" },
  { label: "已取消", value: "cancelled" }
];

export default function RunsPage() {
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [status, setStatus] = useState("");
  const [isLoading, setIsLoading] = useState(true);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const loadRuns = useCallback(async () => {
    try {
      setRuns(await getRuns({ status }));
      setErrorMessage(null);
    } catch (error) {
      setErrorMessage(getErrorMessage(error));
    } finally {
      setIsLoading(false);
    }
  }, [status]);

  useEffect(() => {
    setIsLoading(true);
    void loadRuns();
    const timer = window.setInterval(() => void loadRuns(), 5000);
    return () => window.clearInterval(timer);
  }, [loadRuns]);

  return (
    <div className={styles.page}>
      <TerminalFrame
        actions={
          <div className={styles.toolbar}>
            <label className={styles.filterLabel}>
              状态
              <select value={status} onChange={(event) => setStatus(event.target.value)}>
                {RUN_STATUSES.map((item) => <option key={item.label} value={item.value}>{item.label}</option>)}
              </select>
            </label>
            <button type="button" onClick={() => void loadRuns()}>刷新</button>
          </div>
        }
        bodyClassName={styles.listBody}
        className={styles.listFrame}
        label="Durable Agent Runtime"
        title={isLoading ? "正在加载运行记录" : `运行数量 ${runs.length}`}
      >
        {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}
        {isLoading ? <p className={styles.placeholder}>正在读取新编排器的持久化运行</p> : null}
        {!isLoading && runs.length === 0 ? <p className={styles.placeholder}>当前筛选条件下没有运行记录。</p> : null}
        <ul className={styles.list}>
          {runs.map((run) => (
            <li className={styles.item} key={run.id}>
              <div className={styles.itemHeader}>
                <div>
                  <strong>{run.objective}</strong>
                  <p className={styles.identifier}>运行 {truncateId(run.id, 8, 4)}</p>
                </div>
                <StatusChip status={run.status} />
              </div>
              <div className={styles.meta}>
                <MetaRow label="当前步骤" value={run.current_step || "等待调度"} />
                <MetaRow label="步骤进度" value={`${run.completed_step_count}/${run.step_count}`} />
                <MetaRow label="失败步骤" value={String(run.failed_step_count)} />
                <MetaRow label="更新时间" value={toIsoSeconds(run.updated_at)} />
              </div>
              <div className={styles.actions}>
                <Link className={styles.viewButton} href={`/runs/${run.id}`}>查看运行</Link>
                {run.pending_approval_id ? <Link className={styles.approvalButton} href="/approvals">处理审批</Link> : null}
              </div>
            </li>
          ))}
        </ul>
      </TerminalFrame>
    </div>
  );
}
