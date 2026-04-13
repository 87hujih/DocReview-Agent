import { getTaskEvents } from "./tasks";

describe("tasks api", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("loads task events from the task events endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          events: [
            {
              created_at: "2026-04-12T10:00:00Z",
              event_type: "step.started",
              id: "event-1",
              level: "info",
              message: "规划步骤开始",
              payload: { step_name: "planner" },
              run_id: null,
              source: "orchestrator",
              step_name: "planner",
              task_id: "task-1"
            }
          ]
        }),
        {
          headers: { "Content-Type": "application/json" },
          status: 200
        }
      )
    );
    vi.stubGlobal("fetch", fetchMock);

    const events = await getTaskEvents("task-1");

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:18080/api/tasks/task-1/events",
      expect.objectContaining({
        cache: "no-store"
      })
    );
    expect(events).toHaveLength(1);
    expect(events[0].event_type).toBe("step.started");
    expect(events[0].payload).toEqual({ step_name: "planner" });
  });
});
