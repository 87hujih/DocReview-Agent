import { getResourceExportURL, getResourceTaskContext } from "./resources";

describe("resources api", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("builds the current resource version export URL", () => {
    expect(getResourceExportURL("resource 1")).toBe("/api/resources/resource%201/export");
  });

  it("loads resource task context from the task-context endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          capabilities: {
            blocking_reason: null,
            can_create_task: true,
            can_search_citations: true
          },
          current_version: {
            created_at: "2026-04-15T08:10:00Z",
            id: "version-1",
            source: "upload",
            version_number: 3
          },
          resource: {
            created_at: "2026-04-15T08:00:00Z",
            id: "resource-1",
            source_type: "markdown",
            title: "员工手册"
          }
        }),
        {
          headers: { "Content-Type": "application/json" },
          status: 200
        }
      )
    );
    vi.stubGlobal("fetch", fetchMock);

    const response = await getResourceTaskContext("resource-1");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/resources/resource-1/task-context",
      expect.objectContaining({
        cache: "no-store"
      })
    );
    expect(response.capabilities.can_create_task).toBe(true);
    expect(response.current_version?.version_number).toBe(3);
  });
});
