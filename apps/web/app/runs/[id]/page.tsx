"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";

import { MetaRow } from "../../../components/ui/meta-row";
import { StatusChip } from "../../../components/ui/status-chip";
import { TerminalFrame } from "../../../components/ui/terminal-frame";
import { getRun, type RunDetail } from "../../../lib/api/runs";
import { getErrorMessage, toIsoSeconds, truncateId } from "../../../lib/terminal";
import styles from "./page.module.css";

export default function RunDetailPage() {
  const params = useParams<{ id: string }>();
  const runID = params.id;
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const loadRun = useCallback(async () => {
    try {
      setDetail(await getRun(runID));
      setErrorMessage(null);
    } catch (error) {
      setErrorMessage(getErrorMessage(error));
    }
  }, [runID]);

  useEffect(() => {
    void loadRun();
    const timer = window.setInterval(() => void loadRun(), 5000);
    return () => window.clearInterval(timer);
  }, [loadRun]);

  if (!detail) {
    return (
      <div className={styles.page}>
        <TerminalFrame label="运行详情" title={errorMessage ? "加载失败" : "正在加载"}>
          <p className={errorMessage ? styles.error : styles.placeholder}>{errorMessage || "正在读取 durable Runtime 诊断投影"}</p>
        </TerminalFrame>
      </div>
    );
  }

  const { run } = detail;
  return (
    <div className={styles.page}>
      <TerminalFrame
        actions={<Link className={styles.backButton} href="/runs">返回运行列表</Link>}
        label="运行详情"
        meta={<StatusChip status={run.status} />}
        title={run.objective}
      >
        {errorMessage ? <p className={styles.error}>{errorMessage}</p> : null}
        <div className={styles.meta}>
          <MetaRow label="运行 ID" value={run.id} />
          <MetaRow label="当前步骤" value={run.current_step || "无"} />
          <MetaRow label="资源 ID" value={run.resource_id || "未关联"} />
          <MetaRow label="会话 ID" value={run.session_id || "未关联"} />
          <MetaRow label="请求 ID" value={run.request_id || "未提供"} />
          <MetaRow label="创建时间" value={toIsoSeconds(run.created_at)} />
          <MetaRow label="更新时间" value={toIsoSeconds(run.updated_at)} />
        </div>
      </TerminalFrame>

      <TerminalFrame label="编排进度" title={`步骤 ${detail.steps.length}`}>
        <ol className={styles.timeline}>
          {detail.steps.map((step) => (
            <li key={step.id}>
              <div className={styles.rowHeader}>
                <strong>{step.step_type} / {step.step_key}</strong>
                <StatusChip status={step.status} />
              </div>
              <MetaRow label="尝试次数" value={`${step.attempt_count}/${step.max_attempts}`} />
              <MetaRow label="更新时间" value={toIsoSeconds(step.updated_at)} />
            </li>
          ))}
        </ol>
      </TerminalFrame>

      <TerminalFrame label="工具执行" title={`调用 ${detail.tool_calls.length}`}>
        <ul className={styles.timeline}>
          {detail.tool_calls.map((call) => (
            <li key={call.id}>
              <div className={styles.rowHeader}>
                <strong>{call.tool_name}@{call.tool_version}</strong>
                <StatusChip status={call.status} />
              </div>
              <MetaRow label="调用 ID" value={truncateId(call.id, 8, 4)} />
              <MetaRow label="错误类别" value={call.error_category || "无"} />
            </li>
          ))}
          {detail.tool_calls.length === 0 ? <li className={styles.placeholder}>尚无工具调用。</li> : null}
        </ul>
      </TerminalFrame>

      {detail.approvals.length > 0 ? (
        <TerminalFrame actions={<Link className={styles.backButton} href="/approvals">打开审批队列</Link>} label="审批" title={`审批 ${detail.approvals.length}`}>
          <ul className={styles.timeline}>
            {detail.approvals.map((approval) => (
              <li key={approval.id}>
                <div className={styles.rowHeader}><strong>{approval.tool_name}</strong><StatusChip status={approval.status} /></div>
              </li>
            ))}
          </ul>
        </TerminalFrame>
      ) : null}

      {detail.findings.length > 0 ? (
        <TerminalFrame label="运行告警" title={`告警 ${detail.findings.length}`}>
          <ul className={styles.findings}>
            {detail.findings.map((finding) => <li key={`${finding.code}-${finding.message}`}>[{finding.severity}] {finding.message}</li>)}
          </ul>
        </TerminalFrame>
      ) : null}
    </div>
  );
}
