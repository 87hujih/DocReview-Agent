import { render, screen } from "@testing-library/react";

import ApprovalsPage from "../app/approvals/page";

vi.mock("../lib/api/approvals", () => ({
  getApprovals: vi.fn().mockResolvedValue([
    {
      id: "approval-1",
      run_id: "00000000-0000-4000-8000-000000000001",
      step_id: "step-1",
      objective: "审阅合同",
      tool_name: "document.patch",
      tool_version: "v1",
      reason: "需要应用建议修订",
      status: "pending",
      resources: [],
      payload: {},
      created_at: "2026-04-12T10:00:00Z"
    }
  ]),
  approveApproval: vi.fn(),
  rejectApproval: vi.fn()
}));

it("renders pending approvals from the durable runtime", async () => {
  render(<ApprovalsPage />);

  expect(await screen.findByText("审阅合同")).toBeInTheDocument();
  expect(screen.getByText("document.patch@v1")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "批准" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "拒绝" })).toBeInTheDocument();
});
