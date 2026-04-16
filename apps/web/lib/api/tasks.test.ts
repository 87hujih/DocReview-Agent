import { getDiffPreviewArtifact, getTaskEvents, type TaskArtifact } from "./tasks";

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

  it("preserves explicit section_occurrence while filtering invalid sections", () => {
    const artifacts: TaskArtifact[] = [
      {
        content: {
          sections: [
            {
              citation_ids: ["cite_1"],
              original: "old",
              reason: "clarify",
              revised: "new",
              section_occurrence: 2,
              section_title: "Policy Updates"
            },
            {
              citation_ids: ["cite_2"],
              original: "ignored",
              reason: "missing title",
              revised: "ignored",
              section_occurrence: 3,
              section_title: "   "
            }
          ]
        },
        created_at: "2026-04-15T10:00:00Z",
        id: "artifact-1",
        artifact_type: "diff_preview"
      }
    ];

    expect(getDiffPreviewArtifact(artifacts)).toEqual({
      sections: [
        {
          citation_ids: ["cite_1"],
          original: "old",
          reason: "clarify",
          revised: "new",
          section_occurrence: 2,
          section_title: "Policy Updates"
        }
      ]
    });
  });

  it("keeps legacy diff preview sections without section_occurrence and normalizes missing citation_ids", () => {
    const artifacts: TaskArtifact[] = [
      {
        content: {
          sections: [
            {
              original: "old",
              reason: "clarify",
              revised: "new",
              section_title: "Policy Updates"
            }
          ]
        },
        created_at: "2026-04-15T10:00:00Z",
        id: "artifact-1",
        artifact_type: "diff_preview"
      }
    ];

    expect(getDiffPreviewArtifact(artifacts)).toEqual({
      sections: [
        {
          citation_ids: [],
          original: "old",
          reason: "clarify",
          revised: "new",
          section_title: "Policy Updates"
        }
      ]
    });
  });

  it("ignores invalid section_occurrence values from diff preview artifacts", () => {
    const artifacts: TaskArtifact[] = [
      {
        content: {
          sections: [
            {
              citation_ids: ["cite_1", 123, null],
              original: "old",
              reason: "clarify",
              revised: "new",
              section_occurrence: 0,
              section_title: "Policy Updates"
            }
          ]
        },
        created_at: "2026-04-15T10:00:00Z",
        id: "artifact-1",
        artifact_type: "diff_preview"
      }
    ];

    expect(getDiffPreviewArtifact(artifacts)).toEqual({
      sections: [
        {
          citation_ids: ["cite_1"],
          original: "old",
          reason: "clarify",
          revised: "new",
          section_title: "Policy Updates"
        }
      ]
    });
  });
});
