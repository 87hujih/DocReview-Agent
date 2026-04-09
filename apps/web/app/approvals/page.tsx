"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

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

export default function ApprovalsPage() {
  const [activeApprovalId, setActiveApprovalId] = useState<string | null>(null);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  async function loadApprovals() {
    try {
      const items = await getApprovals("pending");
      setApprovals(items);
      setErrorMessage(null);
    } catch (error) {
      setErrorMessage(getErrorMessage(error));
    } finally {
      setIsLoading(false);
    }
  }

  useEffect(() => {
    void loadApprovals();
  }, []);

  async function handleApprove(id: string) {
    setActiveApprovalId(id);

    try {
      await approveApproval(id);
      await loadApprovals();
    } catch (error) {
      setErrorMessage(getErrorMessage(error));
    } finally {
      setActiveApprovalId(null);
    }
  }

  async function handleReject(id: string) {
    const reason = window.prompt("请输入拒绝原因：");
    if (reason === null) {
      return;
    }

    const trimmedReason = reason.trim();
    if (!trimmedReason) {
      setErrorMessage("拒绝原因不能为空");
      return;
    }

    setActiveApprovalId(id);

    try {
      await rejectApproval(id, trimmedReason);
      await loadApprovals();
    } catch (error) {
      setErrorMessage(getErrorMessage(error));
    } finally {
      setActiveApprovalId(null);
    }
  }

  return (
    <div className={styles.page}>
      <TerminalFrame
        label="审批队列"
        title="审批中心"
        description="待审批任务按队列列出，可直接查看详情或执行批准、拒绝操作。"
      >
        <p className={styles.banner}>输出 &gt; 正在调用 /api/approvals?status=pending</p>
      </TerminalFrame>

      <TerminalFrame
        label="待处理项"
        title={`队列深度 ${approvals.length}`}
        description="审批状态保持终端高对比风格，避免企业后台式柔和 badge。"
      >
        {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}

        {isLoading ? (
          <p className={styles.placeholder}>正在加载审批队列</p>
        ) : approvals.length === 0 ? (
          <p className={styles.placeholder}>当前没有待审批任务</p>
        ) : (
          <ul className={styles.list}>
            {approvals.map((approval) => {
              const isBusy = activeApprovalId === approval.id;

              return (
                <li key={approval.id} className={styles.item}>
                  <div className={styles.itemHeader}>
                    <StatusChip status={approval.status} />
                    <span className={styles.approvalId}>ID: {truncateId(approval.id, 8, 4)}</span>
                  </div>

                  <div className={styles.meta}>
                    <MetaRow label="task_id" value={truncateId(approval.task_id, 8, 4)} />
                    <MetaRow label="created_at" value={toIsoSeconds(approval.created_at)} />
                  </div>

                  <div className={styles.actions}>
                    <Link className={styles.actionButton} href={`/tasks/${approval.task_id}`}>
                      查看任务
                    </Link>
                    <button className={styles.actionButton} disabled={isBusy} onClick={() => void handleApprove(approval.id)} type="button">
                      {isBusy ? "处理中" : "批准"}
                    </button>
                    <button className={styles.actionButton} disabled={isBusy} onClick={() => void handleReject(approval.id)} type="button">
                      {isBusy ? "处理中" : "拒绝"}
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </TerminalFrame>
    </div>
  );
}
