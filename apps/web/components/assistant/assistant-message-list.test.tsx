import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AssistantMessage, AssistantRenderableMessage } from "../../lib/assistant/types";
import { AssistantMessageList } from "./assistant-message-list";

describe("AssistantMessageList", () => {
  const originalEnv = process.env;

  beforeEach(() => {
    process.env = { ...originalEnv, NEXT_PUBLIC_API_URL: "http://127.0.0.1:18080" };
  });

  afterEach(() => {
    process.env = originalEnv;
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  function createTextMessage(
    id: string,
    role: "assistant" | "user",
    content: string,
    overrides?: Partial<AssistantMessage>
  ): AssistantRenderableMessage {
    return {
      created_at: "2026-04-18T10:00:00Z",
      id,
      kind: "text",
      payload: { content },
      role,
      sequence_no: 1,
      ...overrides
    } as AssistantMessage;
  }

  it("shows original file download link when session file has file_id", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          {
            created_at: "2026-04-13T10:00:00Z",
            id: "message-file",
            kind: "session_file",
            payload: {
              file_id: "file-123",
              file_name: "学生守则.pdf",
              resource_id: "resource-1",
              resource_title: "学生守则",
              source_type: "upload",
              status: "ready"
            },
            role: "assistant",
            sequence_no: 1
          } satisfies AssistantMessage
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.getByRole("link", { name: "下载原文件" })).toHaveAttribute(
      "href",
      "http://127.0.0.1:18080/api/files/file-123/download"
    );
  });

  it("does not show original file download link when file_id is missing", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          {
            created_at: "2026-04-13T10:00:00Z",
            id: "message-file",
            kind: "session_file",
            payload: {
              file_name: "学生守则.pdf",
              resource_id: "resource-1",
              resource_title: "学生守则",
              source_type: "upload",
              status: "ready"
            },
            role: "assistant",
            sequence_no: 1
          } satisfies AssistantMessage
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.queryByRole("link", { name: "下载原文件" })).not.toBeInTheDocument();
  });

  it("disables consumed task suggestions once a task_created message references the same suggestion", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          {
            created_at: "2026-04-13T10:00:00Z",
            id: "message-suggestion",
            kind: "task_suggestion",
            payload: {
              action_label: "确认创建任务",
              can_create: true,
              instruction: "请修订第二章",
              resource_id: "resource-1",
              resource_label: "学生守则 · upload",
              status_message: "资源已明确，可以创建任务。",
              title: "建议创建任务"
            },
            role: "assistant",
            sequence_no: 1
          } satisfies AssistantMessage,
          {
            created_at: "2026-04-13T10:01:00Z",
            id: "message-created",
            kind: "task_created",
            payload: {
              detail_url: "/tasks/task-1",
              instruction: "请修订第二章",
              resource_id: "resource-1",
              status: "pending",
              suggestion_message_id: "message-suggestion",
              task_id: "task-1"
            },
            role: "assistant",
            sequence_no: 2
          } satisfies AssistantMessage
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.getByRole("button", { name: "任务已创建" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "确认创建任务" })).not.toBeInTheDocument();
  });

  it("renders completed task_status card with result action", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          {
            created_at: "2026-04-16T10:00:00Z",
            id: "message-task-status",
            kind: "task_status",
            payload: {
              detail_url: "/tasks/task-1?session=session-1",
              instruction: "请修订第二章",
              resource_id: "resource-1",
              result_url: "/resources/resource-1?session=session-1",
              status: "completed",
              status_message: "最终修订版本已写入资源库，可以查看结果或继续对话。",
              task_id: "task-1",
              title: "任务已完成"
            },
            role: "assistant",
            sequence_no: 1
          } satisfies AssistantMessage
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.getByText("任务已完成")).toBeInTheDocument();
    expect(screen.getByText("最终修订版本已写入资源库，可以查看结果或继续对话。")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "查看修订结果" })).toHaveAttribute(
      "href",
      "/resources/resource-1?session=session-1"
    );
    expect(screen.getByRole("link", { name: "打开任务详情" })).toHaveAttribute(
      "href",
      "/tasks/task-1?session=session-1"
    );
  });

  it("renders failed task_status card with detail action only", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          {
            created_at: "2026-04-16T10:00:00Z",
            id: "message-task-status",
            kind: "task_status",
            payload: {
              detail_url: "/tasks/task-1?session=session-1",
              instruction: "请修订第二章",
              resource_id: "resource-1",
              status: "failed",
              status_message: "任务未能完成，请打开详情查看失败原因。",
              task_id: "task-1",
              title: "任务执行失败"
            },
            role: "assistant",
            sequence_no: 1
          } satisfies AssistantMessage
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.getByText("任务执行失败")).toBeInTheDocument();
    expect(screen.getByText("任务未能完成，请打开详情查看失败原因。")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "打开任务详情" })).toHaveAttribute(
      "href",
      "/tasks/task-1?session=session-1"
    );
    expect(screen.queryByRole("link", { name: "查看修订结果" })).not.toBeInTheDocument();
  });

  it("renders copy buttons for assistant and user text messages only", () => {
    const { container } = render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          createTextMessage("assistant-text", "assistant", "请看这段回复"),
          createTextMessage("user-text", "user", "这是我的问题"),
          {
            created_at: "2026-04-18T10:02:00Z",
            id: "message-task-status",
            kind: "task_status",
            payload: {
              detail_url: "/tasks/task-1",
              instruction: "请修订第二章",
              resource_id: "resource-1",
              status: "failed",
              status_message: "任务未能完成，请打开详情查看失败原因。",
              task_id: "task-1",
              title: "任务执行失败"
            },
            role: "assistant",
            sequence_no: 3
          } satisfies AssistantMessage
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.getAllByRole("button", { name: "复制消息" })).toHaveLength(2);
    expect(screen.queryByText("复制")).not.toBeInTheDocument();
    expect(screen.queryByText("助手")).not.toBeInTheDocument();
    expect(screen.queryByText("你")).not.toBeInTheDocument();
    expect(container.querySelector("[data-copy-anchor='assistant-footer']")).not.toBeNull();
    expect(container.querySelector("[data-copy-anchor='user-rail']")).not.toBeNull();
  });

  it("renders assistant markdown after completion but keeps user text literal", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          createTextMessage(
            "assistant-markdown",
            "assistant",
            "# 一级标题\n\n- 第一项\n\n> 引用内容\n\n这里有 `行内代码`。\n\n```ts\nconst value = 1;\n```\n\n[官网](https://example.com)"
          ),
          createTextMessage("user-literal", "user", "**不要加粗我**")
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.getByRole("heading", { level: 1, name: "一级标题" })).toBeInTheDocument();
    expect(screen.getByText("第一项").closest("li")).not.toBeNull();
    expect(screen.getByText("引用内容").closest("blockquote")).not.toBeNull();
    expect(screen.getByText("行内代码", { selector: "code" })).toBeInTheDocument();
    expect(screen.getByText("const value = 1;", { selector: "code" }).closest("pre")).not.toBeNull();
    expect(screen.getByRole("link", { name: "官网" })).toHaveAttribute("href", "https://example.com");
    expect(screen.getByText("**不要加粗我**")).toBeInTheDocument();
    expect(screen.queryByText("不要加粗我", { selector: "strong" })).not.toBeInTheDocument();
  });

  it("renders streaming assistant messages as markdown", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          {
            created_at: "2026-04-18T10:00:00Z",
            id: "streaming-assistant",
            kind: "local_text",
            local_state: "streaming",
            payload: { content: "# 流式标题\n\n- 流式列表项" },
            role: "assistant",
            sequence_no: 1
          }
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.getByRole("heading", { level: 1, name: "流式标题" })).toBeInTheDocument();
    expect(screen.getByText("流式列表项").closest("li")).not.toBeNull();
  });

  it("does not render a user avatar placeholder", () => {
    const { container } = render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[createTextMessage("user-text", "user", "这是我的问题")]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(container.querySelector("[aria-hidden='true']")).toBeNull();
  });

  it("shows copy success feedback for text messages", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(window.navigator, "clipboard", {
      configurable: true,
      value: { writeText }
    });

    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[createTextMessage("assistant-text", "assistant", "原始 **markdown** 文本")]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    fireEvent.click(screen.getByRole("button", { name: "复制消息" }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("原始 **markdown** 文本"));
    expect(screen.getByRole("button", { name: "复制成功" })).toBeInTheDocument();
  });

  it("shows copy failure feedback when clipboard write fails", async () => {
    const writeText = vi.fn().mockRejectedValue(new Error("clipboard blocked"));
    Object.defineProperty(window.navigator, "clipboard", {
      configurable: true,
      value: { writeText }
    });

    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[createTextMessage("assistant-text", "assistant", "原始 **markdown** 文本")]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    fireEvent.click(screen.getByRole("button", { name: "复制消息" }));

    await waitFor(() => expect(screen.getByRole("button", { name: "复制失败" })).toBeInTheDocument());
  });

  it("renders the copy action with the sourced copy icon", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[createTextMessage("assistant-text", "assistant", "原始 **markdown** 文本")]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    const button = screen.getByRole("button", { name: "复制消息" });
    expect(button.querySelector('[data-copy-icon="copy"]')).not.toBeNull();
  });
});

