import { getRun, getRuns } from "./runs";

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200 });
}

describe("durable runs api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("loads a bounded filtered run list", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ runs: [] }));
    vi.stubGlobal("fetch", fetchMock);

    await getRuns({ status: "running", limit: 25 });

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/agent/runs?limit=25&status=running",
      expect.objectContaining({ cache: "no-store" })
    );
  });

  it("loads a durable run detail", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ run: { id: "run-1" } }));
    vi.stubGlobal("fetch", fetchMock);

    await getRun("run-1");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/agent/runs/run-1",
      expect.objectContaining({ cache: "no-store" })
    );
  });
});
