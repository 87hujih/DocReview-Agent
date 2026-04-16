import { approveApproval, getApproval, getApprovals, getJob, rejectApproval } from "./approvals";

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
    status: 200
  });
}

describe("approvals api", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("loads pending approvals list", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        approvals: [
          {
            created_at: "2026-04-12T10:00:00Z",
            decided_at: null,
            id: "approval-1",
            reject_reason: null,
            status: "pending",
            task_id: "task-1"
          }
        ]
      })
    );
    vi.stubGlobal("fetch", fetchMock);

    const approvals = await getApprovals("pending");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/approvals?status=pending",
      expect.objectContaining({
        cache: "no-store"
      })
    );
    expect(approvals).toEqual([
      expect.objectContaining({
        id: "approval-1",
        status: "pending",
        task_id: "task-1"
      })
    ]);
  });

  it("loads a single approval by id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        approval: {
          created_at: "2026-04-12T10:00:00Z",
          decided_at: null,
          id: "approval-1",
          reject_reason: null,
          status: "pending",
          task_id: "task-1"
        }
      })
    );
    vi.stubGlobal("fetch", fetchMock);

    const approval = await getApproval("approval-1");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/approvals/approval-1",
      expect.objectContaining({
        cache: "no-store"
      })
    );
    expect(approval.id).toBe("approval-1");
    expect(approval.reject_reason).toBeNull();
  });

  it("approves an approval", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        approval: {
          created_at: "2026-04-12T10:00:00Z",
          decided_at: "2026-04-12T10:01:00Z",
          id: "approval-1",
          reject_reason: null,
          status: "approved",
          task_id: "task-1"
        }
      })
    );
    vi.stubGlobal("fetch", fetchMock);

    const approval = await approveApproval("approval-1");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/approvals/approval-1/approve",
      expect.objectContaining({
        cache: "no-store",
        method: "POST"
      })
    );
    expect(approval).toEqual(
      expect.objectContaining({
        id: "approval-1",
        status: "approved"
      })
    );
  });

  it("rejects an approval with JSON reason", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        approval: {
          created_at: "2026-04-12T10:00:00Z",
          decided_at: "2026-04-12T10:01:00Z",
          id: "approval-1",
          reject_reason: "需要补充依据",
          status: "rejected",
          task_id: "task-1"
        }
      })
    );
    vi.stubGlobal("fetch", fetchMock);

    const approval = await rejectApproval("approval-1", "需要补充依据");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/approvals/approval-1/reject",
      expect.objectContaining({
        body: JSON.stringify({ reason: "需要补充依据" }),
        cache: "no-store",
        method: "POST"
      })
    );
    expect(approval).toEqual(
      expect.objectContaining({
        id: "approval-1",
        reject_reason: "需要补充依据",
        status: "rejected"
      })
    );
  });

  it("loads a single execution job by id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        job: {
          approval_id: "approval-1",
          completed_at: null,
          created_at: "2026-04-12T10:00:00Z",
          error_message: null,
          id: "job-1",
          new_version_id: null,
          started_at: null,
          status: "pending",
          task_id: "task-1"
        }
      })
    );
    vi.stubGlobal("fetch", fetchMock);

    const job = await getJob("job-1");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/jobs/job-1",
      expect.objectContaining({
        cache: "no-store"
      })
    );
    expect(job.id).toBe("job-1");
    expect(job.approval_id).toBe("approval-1");
  });
});
