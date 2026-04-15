import React from "react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import ApprovalsPage from "../app/approvals/page";
import { approveApproval, getApprovals, rejectApproval } from "../lib/api/approvals";

vi.mock("../lib/api/approvals", () => ({
  approveApproval: vi.fn(),
  getApprovals: vi.fn(),
  rejectApproval: vi.fn()
}));

const mockedGetApprovals = vi.mocked(getApprovals);
const mockedApproveApproval = vi.mocked(approveApproval);
const mockedRejectApproval = vi.mocked(rejectApproval);

function makeApproval(overrides: Partial<Awaited<ReturnType<typeof getApprovals>>[number]> = {}) {
  return {
    created_at: "2026-04-15T08:00:00Z",
    decided_at: null,
    id: "approval-1",
    reject_reason: null,
    status: "pending",
    task_id: "task-1",
    ...overrides
  };
}

function cssRule(source: string, selector: string): string {
  const match = source.match(new RegExp(`\\${selector}\\s*{[^}]+}`));
  return match?.[0] ?? "";
}

describe("ApprovalsPage", () => {
  beforeEach(() => {
    mockedGetApprovals.mockReset();
    mockedApproveApproval.mockReset();
    mockedRejectApproval.mockReset();
  });

  it("does not expose the api debug banner after loading approvals", async () => {
    mockedGetApprovals.mockResolvedValue([]);

    render(<ApprovalsPage />);

    await waitFor(() => {
      expect(mockedGetApprovals).toHaveBeenCalledWith("pending");
    });

    expect(screen.queryByText("输出 > 正在调用 /api/approvals?status=pending")).not.toBeInTheDocument();
  });

  it("shows a real error state instead of pretending the queue is empty", async () => {
    mockedGetApprovals.mockRejectedValue(new Error("审批接口失败"));

    render(<ApprovalsPage />);

    expect(await screen.findByText("错误 > 审批接口失败")).toBeInTheDocument();
    expect(screen.queryByText("当前没有待审批任务")).not.toBeInTheDocument();
    expect(screen.queryByText(/^队列深度 0$/)).not.toBeInTheDocument();
  });

  it("keeps approval cards content-sized when the pending queue gets shorter", () => {
    const pageCss = readFileSync(join(process.cwd(), "app", "approvals", "page.module.css"), "utf8");
    const listBodyRule = cssRule(pageCss, ".listBody");
    const listRule = cssRule(pageCss, ".list");

    expect(listBodyRule).toContain("align-content: start;");
    expect(listRule).toContain("align-content: start;");
    expect(listRule).toContain("grid-auto-rows: max-content;");
  });

  it("removes the approval card after approve succeeds even if queue refresh fails", async () => {
    mockedGetApprovals.mockResolvedValueOnce([makeApproval()]).mockRejectedValueOnce(new Error("刷新失败"));
    mockedApproveApproval.mockResolvedValue(makeApproval({ decided_at: "2026-04-15T08:01:00Z", status: "approved" }));

    render(<ApprovalsPage />);

    const approveButton = await screen.findByText("批准");
    fireEvent.click(approveButton);

    await waitFor(() => {
      expect(screen.queryByText("批准")).not.toBeInTheDocument();
    });

    expect(screen.getByText("当前没有待审批任务")).toBeInTheDocument();
    expect(screen.getByText(/审批已提交|刷新队列失败/)).toBeInTheDocument();
  });

  it("keeps the reject reason draft when reject request fails", async () => {
    mockedGetApprovals.mockResolvedValueOnce([makeApproval()]);
    mockedRejectApproval.mockRejectedValueOnce(new Error("拒绝失败"));

    render(<ApprovalsPage />);

    const rejectButton = await screen.findByText("拒绝");
    fireEvent.click(rejectButton);

    const rejectInput = screen.getByPlaceholderText("输入拒绝原因（Enter 确认，Esc 取消）");
    fireEvent.change(rejectInput, { target: { value: "需要补充依据" } });
    fireEvent.click(screen.getByText("确认拒绝"));

    expect(await screen.findByText("错误 > 拒绝失败")).toBeInTheDocument();
    expect(screen.getByDisplayValue("需要补充依据")).toBeInTheDocument();
    expect(screen.getByText("确认拒绝")).toBeInTheDocument();
  });
});
