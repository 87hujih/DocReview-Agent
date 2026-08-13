import { approveApproval, getApproval, getApprovals, rejectApproval } from "./approvals";

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
    status: 200
  });
}

describe("durable approvals api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("loads bounded pending approvals from the agent runtime", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ approvals: [] }));
    vi.stubGlobal("fetch", fetchMock);

    await getApprovals("pending");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/agent/approvals?limit=50&status=pending",
      expect.objectContaining({ cache: "no-store" })
    );
  });

  it("loads one typed approval", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ approval: { id: "approval-1" } }));
    vi.stubGlobal("fetch", fetchMock);

    await getApproval("approval-1");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/agent/approvals/approval-1",
      expect.objectContaining({ cache: "no-store" })
    );
  });

  it("sends a reason for both approval decisions", async () => {
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(jsonResponse({ approval: { id: "approval-1", status: "approved" } }))
    );
    vi.stubGlobal("fetch", fetchMock);

    await approveApproval("approval-1", "已核对变更范围");
    await rejectApproval("approval-1", "需要补充依据");

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "http://127.0.0.1:18080/api/agent/approvals/approval-1/approve",
      expect.objectContaining({ body: JSON.stringify({ reason: "已核对变更范围" }), method: "POST" })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "http://127.0.0.1:18080/api/agent/approvals/approval-1/reject",
      expect.objectContaining({ body: JSON.stringify({ reason: "需要补充依据" }), method: "POST" })
    );
  });
});
