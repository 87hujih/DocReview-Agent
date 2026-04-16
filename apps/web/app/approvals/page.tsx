"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";

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
  const [actionMessage, setActionMessage] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [loadErrorMessage, setLoadErrorMessage] = useState<string | null>(null);
  const [rejectingId, setRejectingId] = useState<string | null>(null);
  const [rejectReason, setRejectReason] = useState("");
  const rejectInputRef = useRef<HTMLInputElement>(null);
  const mountedRef = useRef(true);
  const bannerMessage = actionMessage ?? loadErrorMessage;
  const showErrorOnly = !isLoading && approvals.length === 0 && Boolean(loadErrorMessage);
  const frameTitle = isLoading
    ? "正在加载审批队列"
    : showErrorOnly
      ? "加载失败"
      : `队列深度 ${approvals.length}`;

  async function fetchPendingApprovals(): Promise<Approval[]> {
    return getApprovals("pending");
  }

  function removeApprovalFromQueue(id: string) {
    setApprovals((current) => current.filter((approval) => approval.id !== id));
  }

  async function syncQueueAfterDecision(id: string) {
    removeApprovalFromQueue(id);

    try {
      const items = await fetchPendingApprovals();
      if (!mountedRef.current) return;
      setApprovals(items);
      setActionMessage(null);
    } catch (error) {
      if (!mountedRef.current) return;
      setActionMessage(`审批已提交，但刷新队列失败：${getErrorMessage(error)}`);
    }
  }

  useEffect(() => {
    mountedRef.current = true;

    async function loadInitialApprovals() {
      try {
        const items = await fetchPendingApprovals();
        if (!mountedRef.current) return;
        setApprovals(items);
        setActionMessage(null);
        setLoadErrorMessage(null);
      } catch (error) {
        if (!mountedRef.current) return;
        setLoadErrorMessage(getErrorMessage(error));
      } finally {
        if (mountedRef.current) setIsLoading(false);
      }
    }

    void loadInitialApprovals();

    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    if (rejectingId) {
      rejectInputRef.current?.focus();
    }
  }, [rejectingId]);

  async function handleApprove(id: string) {
    setActiveApprovalId(id);
    setActionMessage(null);

    try {
      await approveApproval(id);
      await syncQueueAfterDecision(id);
    } catch (error) {
      setActionMessage(getErrorMessage(error));
    } finally {
      setActiveApprovalId(null);
    }
  }

  function handleRejectClick(id: string) {
    setActionMessage(null);
    setRejectingId(id);
    setRejectReason("");
  }

  function handleRejectCancel() {
    setRejectingId(null);
    setRejectReason("");
  }

  async function handleRejectConfirm(id: string) {
    const trimmed = rejectReason.trim();
    if (!trimmed) {
      setActionMessage("拒绝原因不能为空");
      return;
    }

    setActiveApprovalId(id);
    setActionMessage(null);

    try {
      await rejectApproval(id, trimmed);
      setRejectingId(null);
      setRejectReason("");
      await syncQueueAfterDecision(id);
    } catch (error) {
      setActionMessage(getErrorMessage(error));
    } finally {
      setActiveApprovalId(null);
    }
  }

  return (
    <div className={styles.page}>
      <TerminalFrame
        bodyClassName={styles.listBody}
        className={styles.listFrame}
        label="审批中心"
        title={frameTitle}
      >
        {bannerMessage ? <p className={styles.error}>错误 &gt; {bannerMessage}</p> : null}

        {isLoading ? (
          <p className={styles.placeholder}>正在加载审批队列</p>
        ) : showErrorOnly ? null : approvals.length === 0 ? (
          <p className={styles.placeholder}>当前没有待审批任务</p>
        ) : (
          <ul className={styles.list}>
            {approvals.map((approval) => {
              const isBusy = activeApprovalId === approval.id;
              const isRejecting = rejectingId === approval.id;

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

                  {isRejecting ? (
                    <div className={styles.rejectForm}>
                      <input
                        ref={rejectInputRef}
                        className={styles.rejectInput}
                        disabled={isBusy}
                        onChange={(e) => setRejectReason(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Escape") handleRejectCancel();
                          if (e.key === "Enter" && rejectReason.trim()) {
                            void handleRejectConfirm(approval.id);
                          }
                        }}
                        placeholder="输入拒绝原因（Enter 确认，Esc 取消）"
                        type="text"
                        value={rejectReason}
                      />
                      <div className={styles.rejectActions}>
                        <button
                          className={styles.confirmRejectButton}
                          disabled={isBusy || !rejectReason.trim()}
                          onClick={() => void handleRejectConfirm(approval.id)}
                          type="button"
                        >
                          确认拒绝
                        </button>
                        <button
                          className={styles.cancelButton}
                          onClick={handleRejectCancel}
                          type="button"
                        >
                          取消
                        </button>
                      </div>
                    </div>
                  ) : (
                    <div className={styles.actions}>
                      <Link className={styles.viewButton} href={`/tasks/${approval.task_id}`}>
                        查看任务
                      </Link>
                      <button
                        className={styles.approveButton}
                        disabled={isBusy}
                        onClick={() => void handleApprove(approval.id)}
                        type="button"
                      >
                        {isBusy ? "处理中" : "批准"}
                      </button>
                      <button
                        className={styles.rejectButton}
                        disabled={isBusy}
                        onClick={() => handleRejectClick(approval.id)}
                        type="button"
                      >
                        拒绝
                      </button>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </TerminalFrame>
    </div>
  );
}
