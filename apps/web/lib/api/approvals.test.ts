import { getApproval, getJob } from "./approvals";

describe("approvals api", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("loads a single approval by id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          approval: {
            created_at: "2026-04-12T10:00:00Z",
            decided_at: null,
            id: "approval-1",
            reject_reason: null,
            status: "pending",
            task_id: "task-1"
          }
        }),
        {
          headers: { "Content-Type": "application/json" },
          status: 200
        }
      )
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

  it("loads a single execution job by id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
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
        }),
        {
          headers: { "Content-Type": "application/json" },
          status: 200
        }
      )
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
