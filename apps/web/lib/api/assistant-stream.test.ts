import { streamAssistantConversation, toAssistantTurnError } from "./assistant-stream";

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

  it("parses session_file before message_completed and task_suggestion", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        createResponse(
          sse("session_created", {
            session: {
              created_at: "2026-04-10T10:30:00Z",
              id: "session-1",
              last_message_at: "2026-04-10T10:30:00Z",
              title: "流式会话",
              updated_at: "2026-04-10T10:30:00Z"
            }
          }) +
            sse("session_file", {
              message: {
                created_at: "2026-04-10T10:30:01Z",
                id: "message-file",
                kind: "session_file",
                payload: {
                  file_name: "对话粘贴正文.md",
                  resource_id: "resource-inline",
                  resource_title: "对话粘贴正文",
                  source_type: "inline_text",
                  status: "ready"
                },
                role: "assistant",
                sequence_no: 2
              }
            }) +
            sse("message_started", {}) +
            sse("message_completed", {
              message: {
                created_at: "2026-04-10T10:30:02Z",
                id: "message-2",
                kind: "text",
                payload: {
                  content: "我先看这段正文。"
                },
                role: "assistant",
                sequence_no: 3
              }
            }) +
            sse("task_suggestion", {
              message: {
                created_at: "2026-04-10T10:30:03Z",
                id: "message-3",
                kind: "task_suggestion",
                payload: {
                  action_label: "确认创建任务",
                  can_create: true,
                  instruction: "请把这份简历改成产品经理版本",
                  resource_id: "resource-inline",
                  resource_label: "对话粘贴正文 · inline_text",
                  status_message: "资源已明确，可以创建任务。",
                  title: "建议创建任务"
                },
                role: "assistant",
                sequence_no: 4
              }
            }) +
            sse("done", {})
        )
      )
    );

    const events: Array<{ type: string }> = [];
    const result = await streamAssistantConversation("请直接处理这段正文", {
      onEvent: (event) => {
        events.push({ type: event.type });
      }
    });

    expect(result.status).toBe("completed");
    expect(events.map((event) => event.type)).toEqual([
      "session_created",
      "session_file",
      "message_started",
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

  it("keeps an already-normalized backend offline error unchanged", () => {
    expect(
      toAssistantTurnError({
        code: "backend_offline",
        message: "后端未连接，请确认本地 server 已启动。"
      })
    ).toEqual({
      code: "backend_offline",
      message: "后端未连接，请确认本地 server 已启动。"
    });
  });

  it("keeps an already-normalized timeout error unchanged", () => {
    expect(
      toAssistantTurnError({
        code: "request_timeout",
        message: "请求超时，请稍后重试。"
      })
    ).toEqual({
      code: "request_timeout",
      message: "请求超时，请稍后重试。"
    });
  });

  it("preserves backend offline semantics when the stream error is normalized twice", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch"))
    );

    let thrown: unknown;
    try {
      await streamAssistantConversation("帮我梳理学生手册第二章");
    } catch (error) {
      thrown = error;
    }

    expect(toAssistantTurnError(thrown)).toEqual({
      code: "backend_offline",
      message: "后端未连接，请确认本地 server 已启动。"
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
