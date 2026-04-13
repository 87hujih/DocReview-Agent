import { streamAssistantConversation } from "./assistant-stream";

describe("assistant-stream", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("parses session_created, message_delta, message_completed, task_suggestion and done events", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        createResponse(
          sse("session_created", {
            session: {
              created_at: "2026-04-10T10:30:00Z",
              id: "session-1",
              last_message_at: "2026-04-10T10:30:01Z",
              title: "流式会话",
              updated_at: "2026-04-10T10:30:01Z"
            }
          }) +
            sse("message_started", {}) +
            sse("message_delta", { delta: "当然可以" }) +
            sse("message_completed", {
              message: {
                created_at: "2026-04-10T10:30:01Z",
                id: "message-2",
                kind: "text",
                payload: {
                  content: "当然可以，我们先梳理目标。"
                },
                role: "assistant",
                sequence_no: 2
              }
            }) +
            sse("task_suggestion", {
              message: {
                created_at: "2026-04-10T10:30:02Z",
                id: "message-3",
                kind: "task_suggestion",
                payload: {
                  action_label: "确认创建任务",
                  can_create: true,
                  instruction: "请整理学生手册第二章",
                  resource_label: "学生手册 · upload",
                  status_message: "资源已明确，可以创建任务。",
                  title: "建议创建任务"
                },
                role: "assistant",
                sequence_no: 3
              }
            }) +
            sse("done", {})
        )
      )
    );

    const events: Array<{ type: string }> = [];
    const result = await streamAssistantConversation("帮我梳理学生手册第二章", {
      onEvent: (event) => {
        events.push({ type: event.type });
      }
    });

    expect(result.status).toBe("completed");
    expect(events.map((event) => event.type)).toEqual([
      "session_created",
      "message_started",
      "message_delta",
      "message_completed",
      "task_suggestion",
      "done"
    ]);
  });

  it("returns a stopped result when AbortController aborts the request", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
        if (init?.signal?.aborted) {
          return Promise.reject(new DOMException("The operation was aborted.", "AbortError"));
        }

        return Promise.reject(new Error("expected aborted signal"));
      })
    );

    const controller = new AbortController();
    controller.abort();

    const result = await streamAssistantConversation("帮我梳理学生手册第二章", {
      signal: controller.signal
    });

    expect(result.status).toBe("stopped");
    expect(result.error.code).toBe("generation_stopped");
  });

  it("classifies network failures as backend offline", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch"))
    );

    await expect(streamAssistantConversation("帮我梳理学生手册第二章")).rejects.toMatchObject({
      code: "backend_offline"
    });
  });

  it("classifies http 5xx as a structured service error", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        createResponse(`{"error":"服务器异常"}`, {
          headers: {
            "Content-Type": "application/json"
          },
          status: 503
        })
      )
    );

    await expect(streamAssistantConversation("帮我梳理学生手册第二章")).rejects.toMatchObject({
      code: "service_error",
      message: "服务器异常"
    });
  });

  it("classifies assistant_empty_reply as a dedicated empty reply error", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        createResponse(
          sse("error", {
            code: "assistant_empty_reply",
            message: "本轮没有生成可展示内容。"
          })
        )
      )
    );

    await expect(streamAssistantConversation("帮我梳理学生手册第二章")).rejects.toMatchObject({
      code: "assistant_empty_reply",
      message: "本轮没有生成可展示内容。"
    });
  });
});

function createResponse(
  body: string,
  init: {
    headers?: Record<string, string>;
    status?: number;
  } = {}
): Response {
  return new Response(body, {
    headers: {
      "Content-Type": "text/event-stream; charset=utf-8",
      ...init.headers
    },
    status: init.status ?? 200
  });
}

function sse(eventType: string, payload: unknown): string {
  return `event: ${eventType}\ndata: ${JSON.stringify(payload)}\n\n`;
}
