"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";

import { MetaRow } from "../../components/ui/meta-row";
import { StatusChip } from "../../components/ui/status-chip";
import { TerminalFrame } from "../../components/ui/terminal-frame";
import {
  approveApproval,
  getApprovals,
  rejectApproval,
  type Approval
} from "../../lib/api/approvals";
import { getErrorMessage, toIsoSeconds, truncateId } from "../../lib/terminal";
import styles from "./page.module.css";

const APPROVAL_STATUSES = [
  { label: "待处理", value: "pending" },
  { label: "全部", value: "" },
  { label: "已批准", value: "approved" },
  { label: "已拒绝", value: "rejected" },
  { label: "已取消", value: "cancelled" }
];

export default function ApprovalsPage() {
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [status, setStatus] = useState("pending");
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const [busyID, setBusyID] = useState<string | null>(null);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const loadApprovals = useCallback(async () => {
    setIsLoading(true);
    try {
      setApprovals(await getApprovals(status));
      setErrorMessage(null);
    } catch (error) {
      setErrorMessage(getErrorMessage(error));
    } finally {
      setIsLoading(false);
    }
  }, [status]);

  useEffect(() => {
    void loadApprovals();
  }, [loadApprovals]);

  async function decide(approval: Approval, decision: "approve" | "reject") {
    const reason = (reasons[approval.id] || "").trim();
    if (!reason) {
      setErrorMessage("请先填写决策理由。");
      return;
    }
    setBusyID(approval.id);
    try {
      if (decision === "approve") {
        await approveApproval(approval.id, reason);
      } else {
        await rejectApproval(approval.id, reason);
      }
      setReasons((current) => ({ ...current, [approval.id]: "" }));
      await loadApprovals();
    } catch (error) {
      setErrorMessage(getErrorMessage(error));
    } finally {
      setBusyID(null);
    }
  }

  return (
    <div className={styles.page}>
      <TerminalFrame
        actions={
          <div className={styles.toolbar}>
            <label className={styles.filterLabel}>
              状态
              <select value={status} onChange={(event) => setStatus(event.target.value)}>
                {APPROVAL_STATUSES.map((item) => (
                  <option key={item.label} value={item.value}>{item.label}</option>
                ))}
              </select>
            </label>
            <button type="button" onClick={() => void loadApprovals()}>刷新</button>
          </div>
        }
        bodyClassName={styles.listBody}
        className={styles.listFrame}
        label="类型化工具审批"
        title={isLoading ? "正在加载审批" : `审批数量 ${approvals.length}`}
      >
        {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}
        {isLoading ? <p className={styles.placeholder}>正在读取 durable Runtime 审批队列</p> : null}
        {!isLoading && approvals.length === 0 ? <p className={styles.placeholder}>当前筛选条件下没有审批。</p> : null}

        <ul className={styles.list}>
          {approvals.map((approval) => {
            const pending = approval.status === "pending";
            const busy = busyID === approval.id;
            return (
              <li className={styles.item} key={approval.id}>
                <div className={styles.itemHeader}>
                  <div>
                    <strong>{approval.objective || approval.tool_name}</strong>
                    <p className={styles.approvalId}>审批 {truncateId(approval.id, 8, 4)}</p>
                  </div>
                  <StatusChip status={approval.status} />
                </div>

                <div className={styles.meta}>
                  <MetaRow label="工具" value={`${approval.tool_name}@${approval.tool_version}`} />
                  <MetaRow label="申请原因" value={approval.reason} />
                  <MetaRow label="运行 ID" value={truncateId(approval.run_id, 8, 4)} />
                  <MetaRow label="创建时间" value={toIsoSeconds(approval.created_at)} />
                  {approval.decision_reason ? <MetaRow label="决策理由" value={approval.decision_reason} /> : null}
                </div>

                <details className={styles.payload}>
                  <summary>查看审批载荷</summary>
                  <pre>{formatPayload({ resources: approval.resources, payload: approval.payload })}</pre>
                </details>

                <div className={styles.actions}>
                  <Link className={styles.viewButton} href={`/runs/${approval.run_id}`}>查看运行</Link>
                  {pending ? (
                    <>
                      <input
                        aria-label={`审批 ${approval.id} 的决策理由`}
                        className={styles.reasonInput}
                        onChange={(event) => setReasons((current) => ({ ...current, [approval.id]: event.target.value }))}
                        placeholder="填写决策理由"
                        value={reasons[approval.id] || ""}
                      />
                      <button className={styles.approveButton} disabled={busy} onClick={() => void decide(approval, "approve")} type="button">
                        批准
                      </button>
                      <button className={styles.rejectButton} disabled={busy} onClick={() => void decide(approval, "reject")} type="button">
                        拒绝
                      </button>
                    </>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ul>
      </TerminalFrame>
    </div>
  );
}

function formatPayload(value: unknown): string {
  const formatted = JSON.stringify(value, null, 2) || "{}";
  return formatted.length > 4000 ? `${formatted.slice(0, 4000)}\n...` : formatted;
}
