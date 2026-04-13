import React from "react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { render, screen, waitFor } from "@testing-library/react";

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
});
