import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { AssistantShell } from "./assistant-shell";
import { AppChrome } from "../app-chrome";
import {
  confirmAssistantTaskSuggestion,
  deleteAssistantSession,
  getAssistantCapabilities,
  getAssistantSession,
  getAssistantSessions,
  streamAssistantConversation,
  streamAssistantMessage,
  uploadAssistantFile
} from "../../lib/api/assistant";

vi.mock("../../lib/api/assistant", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api/assistant")>(
    "../../lib/api/assistant"
  );
  return {
    ...actual,
    confirmAssistantTaskSuggestion: vi.fn(),
    deleteAssistantSession: vi.fn(),
    getAssistantCapabilities: vi.fn(),
    getAssistantSession: vi.fn(),
    getAssistantSessions: vi.fn(),
    streamAssistantConversation: vi.fn(),
    streamAssistantMessage: vi.fn(),
    uploadAssistantFile: vi.fn()
  };
});

vi.mock("next/navigation", () => ({
  usePathname: () => "/"
}));

const mockedGetAssistantSessions = vi.mocked(getAssistantSessions);
const mockedGetAssistantSession = vi.mocked(getAssistantSession);
const mockedGetAssistantCapabilities = vi.mocked(getAssistantCapabilities);
const mockedStreamAssistantConversation = vi.mocked(streamAssistantConversation);
const mockedStreamAssistantMessage = vi.mocked(streamAssistantMessage);
const mockedUploadAssistantFile = vi.mocked(uploadAssistantFile);
const mockedConfirmAssistantTaskSuggestion = vi.mocked(confirmAssistantTaskSuggestion);
const mockedDeleteAssistantSession = vi.mocked(deleteAssistantSession);

function renderAssistantShell(url = "/") {
  window.history.replaceState({}, "", url);
  const initialSessionId = new URL(window.location.href).searchParams.get("session");

  return render(
    <AppChrome>
      <AssistantShell initialSessionId={initialSessionId} />
    </AppChrome>
  );
}

describe("AssistantShell", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/");
    mockedGetAssistantSessions.mockReset();
    mockedGetAssistantSession.mockReset();
    mockedGetAssistantCapabilities.mockReset();
    mockedStreamAssistantConversation.mockReset();
    mockedStreamAssistantMessage.mockReset();
    mockedUploadAssistantFile.mockReset();
    mockedConfirmAssistantTaskSuggestion.mockReset();
    mockedDeleteAssistantSession.mockReset();
    mockedGetAssistantCapabilities.mockResolvedValue({
      upload: {
        accept: ".md,.txt",
        hint: "支持 md、txt",
        supported_extensions: [".md", ".txt"]
      }
    });
  });

  it("loads assistant upload capabilities and passes them to the composer", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);
    mockedGetAssistantCapabilities.mockResolvedValue({
      upload: {
        accept: ".md,.txt,.pdf",
        hint: "支持 md、txt、pdf",
        supported_extensions: [".md", ".txt", ".pdf"]
      }
    });

    const { container } = renderAssistantShell();

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
      expect(mockedGetAssistantCapabilities).toHaveBeenCalledTimes(1);
    });

    const input = container.querySelector('input[type="file"]');
    if (!input) {
      throw new Error("expected file input to exist");
    }

    expect(input).toHaveAttribute("accept", ".md,.txt,.pdf");
    expect(screen.getByText("请先发送第一条消息后再上传 · 支持 md、txt、pdf")).toBeInTheDocument();
  });

  it("falls back to conservative upload capabilities when the capability request fails", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);
    mockedGetAssistantCapabilities.mockRejectedValue(new Error("capability failed"));

    const { container } = renderAssistantShell();

    await waitFor(() => {
      expect(mockedGetAssistantCapabilities).toHaveBeenCalledTimes(1);
    });

    const input = container.querySelector('input[type="file"]');
    if (!input) {
      throw new Error("expected file input to exist");
    }

    expect(input).toHaveAttribute("accept", ".md,.txt");
    expect(screen.getByText("请先发送第一条消息后再上传 · 支持 md、txt")).toBeInTheDocument();
  });

  it("loads session history but still starts from a blank draft conversation", async () => {
    mockedGetAssistantSessions.mockResolvedValue([
      {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    ]);

    renderAssistantShell();

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
    });

    expect(screen.getByRole("button", { name: "新对话" })).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText("昨天整理学生守则的思路")).toBeInTheDocument();
    });
    expect(screen.getByText("有什么可以帮到你？")).toBeInTheDocument();
  });

  it("loads target session from session query on first render", async () => {
    mockedGetAssistantSessions.mockResolvedValue([
      {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    ]);
    mockedGetAssistantSession.mockResolvedValue({
      messages: [
        {
          created_at: "2026-04-10T10:10:00Z",
          id: "message-1",
          kind: "text",
          payload: {
            content: "这是指定会话里的历史消息"
          },
          role: "assistant",
          sequence_no: 1
        }
      ],
      session: {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    });

    renderAssistantShell("/?session=session-1");

    await waitFor(() => {
      expect(mockedGetAssistantSession).toHaveBeenCalledWith("session-1");
    });
    expect(await screen.findByText("这是指定会话里的历史消息")).toBeInTheDocument();
  });

  it("loads target session from session query even when history list does not include it yet", async () => {
    mockedGetAssistantSessions.mockResolvedValue([
      {
        created_at: "2026-04-10T09:00:00Z",
        id: "session-other",
        last_message_at: "2026-04-10T09:30:00Z",
        title: "另一个会话",
        updated_at: "2026-04-10T09:30:00Z"
      }
    ]);
    mockedGetAssistantSession.mockResolvedValue({
      messages: [
        {
          created_at: "2026-04-10T10:10:00Z",
          id: "message-1",
          kind: "text",
          payload: {
            content: "这是需要恢复的原会话"
          },
          role: "assistant",
          sequence_no: 1
        }
      ],
      session: {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    });

    renderAssistantShell("/?session=session-1");

    await waitFor(() => {
      expect(mockedGetAssistantSession).toHaveBeenCalledWith("session-1");
    });
    expect(await screen.findByText("这是需要恢复的原会话")).toBeInTheDocument();
    expect(window.location.search).toBe("?session=session-1");
  });

  it("collapses and restores the whole left rail instead of only hiding assistant history", async () => {
    mockedGetAssistantSessions.mockResolvedValue([
      {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    ]);

    const { container } = renderAssistantShell();

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "收起侧边栏" })).toBeInTheDocument();
    });

    expect(screen.getByRole("navigation", { name: "主导航" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "新对话" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "收起侧边栏" }));

    expect(screen.getByRole("button", { name: "展开侧边栏" })).toBeInTheDocument();
    expect(container.querySelector(".app-body")?.getAttribute("data-rail-collapsed")).toBe("true");
    expect(screen.queryByRole("navigation", { name: "主导航" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "新对话" })).not.toBeInTheDocument();
  });

  it("streams the first assistant reply inside the draft conversation and replaces the placeholder on completion", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);
    let resolveStream: ((value: { status: "completed" }) => void) | null = null;
    let onEvent: ((event: any) => void) | null = null;

    mockedStreamAssistantConversation.mockImplementation(async (_message, options = {}) => {
      onEvent = options.onEvent || null;
      return new Promise((resolve) => {
        resolveStream = resolve as (value: { status: "completed" }) => void;
      });
    });

    renderAssistantShell();

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "请帮我梳理学生守则第二章" }
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(mockedStreamAssistantConversation).toHaveBeenCalledWith(
        "请帮我梳理学生守则第二章",
        expect.objectContaining({
          onEvent: expect.any(Function),
          signal: expect.any(AbortSignal)
        })
      );
    });

    expect(screen.getAllByText("请帮我梳理学生守则第二章").length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "停止生成" })).toBeInTheDocument();

    await act(async () => {
      onEvent?.({
        session: {
          created_at: "2026-04-10T10:30:00Z",
          id: "session-created",
          last_message_at: "2026-04-10T10:30:00Z",
          title: "请帮我梳理学生守则第二章",
          updated_at: "2026-04-10T10:30:00Z"
        },
        type: "session_created"
      });
      onEvent?.({ type: "message_started" });
      onEvent?.({
        delta: "我先继续和你梳理问题",
        type: "message_delta"
      });
    });

    await waitFor(() => {
      expect(screen.getByText("我先继续和你梳理问题")).toBeInTheDocument();
    });

    await act(async () => {
      onEvent?.({
        message: {
          created_at: "2026-04-10T10:30:01Z",
          id: "message-2",
          kind: "text",
          payload: {
            content: "我先继续和你梳理问题，需要时再一起收敛成任务。"
          },
          role: "assistant",
          sequence_no: 2
        },
        type: "message_completed"
      });
      resolveStream?.({ status: "completed" });
    });

    await waitFor(() => {
      expect(screen.getByText("我先继续和你梳理问题，需要时再一起收敛成任务。")).toBeInTheDocument();
    });
    expect(screen.queryByRole("button", { name: "停止生成" })).not.toBeInTheDocument();
  });

  it("loads full messages after clicking a history item", async () => {
    mockedGetAssistantSessions.mockResolvedValue([
      {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    ]);
    mockedGetAssistantSession.mockResolvedValue({
      messages: [
        {
          created_at: "2026-04-10T10:10:00Z",
          id: "message-1",
          kind: "text",
          payload: {
            content: "昨天那条历史消息"
          },
          role: "assistant",
          sequence_no: 1
        }
      ],
      session: {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    });

    renderAssistantShell();

    const historyButton = await screen.findByRole("button", { name: "打开会话 昨天整理学生守则的思路" });
    fireEvent.click(historyButton);

    await waitFor(() => {
      expect(mockedGetAssistantSession).toHaveBeenCalledWith("session-1");
    });

    await waitFor(() => {
      expect(screen.getByText("昨天那条历史消息")).toBeInTheDocument();
    });
  });

  it("updates session query after selecting a history item", async () => {
    mockedGetAssistantSessions.mockResolvedValue([
      {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    ]);
    mockedGetAssistantSession.mockResolvedValue({
      messages: [
        {
          created_at: "2026-04-10T10:10:00Z",
          id: "message-1",
          kind: "text",
          payload: {
            content: "昨天那条历史消息"
          },
          role: "assistant",
          sequence_no: 1
        }
      ],
      session: {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    });

    renderAssistantShell();

    fireEvent.click(await screen.findByRole("button", { name: "打开会话 昨天整理学生守则的思路" }));

    await waitFor(() => {
      expect(mockedGetAssistantSession).toHaveBeenCalledWith("session-1");
    });
    expect(window.location.search).toBe("?session=session-1");
  });

  it("clears session query after starting a new conversation", async () => {
    mockedGetAssistantSessions.mockResolvedValue([
      {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    ]);
    mockedGetAssistantSession.mockResolvedValue({
      messages: [
        {
          created_at: "2026-04-10T10:10:00Z",
          id: "message-1",
          kind: "text",
          payload: {
            content: "这是指定会话里的历史消息"
          },
          role: "assistant",
          sequence_no: 1
        }
      ],
      session: {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    });

    renderAssistantShell("/?session=session-1");

    await waitFor(() => {
      expect(mockedGetAssistantSession).toHaveBeenCalledWith("session-1");
    });

    fireEvent.click(screen.getByRole("button", { name: "新对话" }));

    await waitFor(() => {
      expect(screen.getByText("有什么可以帮到你？")).toBeInTheDocument();
    });
    expect(window.location.search).toBe("");
  });

  it("stops generation and shows a stopped-turn message", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);
    mockedStreamAssistantConversation.mockImplementation(
      async (_message, options = {}) =>
        new Promise((resolve) => {
          options.signal?.addEventListener(
            "abort",
            () => {
              resolve({
                error: {
                  code: "generation_stopped",
                  message: "已停止生成。"
                },
                status: "stopped"
              });
            },
            { once: true }
          );
        })
    );

    renderAssistantShell();

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "帮我整理今天的学习安排" }
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(mockedStreamAssistantConversation).toHaveBeenCalledTimes(1);
    });

    fireEvent.click(screen.getByRole("button", { name: "停止生成" }));

    await waitFor(() => {
      expect(screen.getByText("已停止生成。")).toBeInTheDocument();
    });
  });

  it("shows backend offline errors inside the message area instead of a generic banner", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);
    mockedStreamAssistantConversation.mockRejectedValue({
      code: "backend_offline",
      message: "后端未连接，请确认本地 server 已启动。"
    });

    renderAssistantShell();

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByLabelText("输入消息"), {
      target: { value: "帮我整理今天的学习安排" }
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(screen.getByText("后端未连接，请确认本地 server 已启动。")).toBeInTheDocument();
    });
    expect(screen.queryByText(/^提示：/)).not.toBeInTheDocument();
  });

  it("keeps history errors at page level while turn errors stay inside the message area", async () => {
    mockedGetAssistantSessions.mockRejectedValue(new Error("历史接口失败"));
    mockedStreamAssistantConversation.mockRejectedValue({
      code: "assistant_empty_reply",
      message: "本轮没有生成可展示内容。"
    });

    renderAssistantShell();

    await waitFor(() => {
      expect(screen.getByText("历史加载失败：历史接口失败")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByLabelText("输入消息"), {
      target: { value: "帮我整理今天的学习安排" }
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(screen.getByText("本轮没有生成可展示内容。")).toBeInTheDocument();
    });
    expect(screen.queryByText(/^提示：本轮没有生成可展示内容。$/)).not.toBeInTheDocument();
  });

  it("marks the original suggestion card as consumed after confirming task creation", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);
    let resolveStream: ((value: { status: "completed" }) => void) | null = null;
    let onEvent: ((event: any) => void) | null = null;

    mockedStreamAssistantConversation.mockImplementation(async (_message, options = {}) => {
      onEvent = options.onEvent || null;
      return new Promise((resolve) => {
        resolveStream = resolve as (value: { status: "completed" }) => void;
      });
    });
    mockedConfirmAssistantTaskSuggestion.mockResolvedValue({
      error_message: null,
      messages: [
        {
          created_at: "2026-04-10T10:30:02Z",
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
          sequence_no: 3
        }
      ],
      session: {
        created_at: "2026-04-10T10:30:00Z",
        id: "session-created",
        last_message_at: "2026-04-10T10:30:02Z",
        title: "请帮我梳理学生守则第二章",
        updated_at: "2026-04-10T10:30:02Z"
      },
      task: {
        created_at: "2026-04-10T10:30:02Z",
        id: "task-1",
        instruction: "请修订第二章",
        resource_id: "resource-1",
        status: "pending"
      }
    });

    renderAssistantShell();

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
      expect(mockedGetAssistantCapabilities).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "请帮我梳理学生守则第二章" }
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(mockedStreamAssistantConversation).toHaveBeenCalledTimes(1);
    });

    await act(async () => {
      onEvent?.({
        session: {
          created_at: "2026-04-10T10:30:00Z",
          id: "session-created",
          last_message_at: "2026-04-10T10:30:00Z",
          title: "请帮我梳理学生守则第二章",
          updated_at: "2026-04-10T10:30:00Z"
        },
        type: "session_created"
      });
      onEvent?.({
        message: {
          created_at: "2026-04-10T10:30:01Z",
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
          sequence_no: 2
        },
        type: "task_suggestion"
      });
      resolveStream?.({ status: "completed" });
    });

    const confirmButton = await screen.findByRole("button", { name: "确认创建任务" });
    fireEvent.click(confirmButton);

    await waitFor(() => {
      expect(mockedConfirmAssistantTaskSuggestion).toHaveBeenCalledWith("message-suggestion");
    });

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "任务已创建" })).toBeDisabled();
    });
    expect(screen.queryByRole("button", { name: "确认创建任务" })).not.toBeInTheDocument();
  });

  it("keeps a fresh draft clean when reset aborts a streaming turn", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);
    mockedStreamAssistantConversation.mockImplementation(
      async (_message, options = {}) =>
        new Promise((resolve) => {
          options.signal?.addEventListener(
            "abort",
            () => {
              resolve({
                error: {
                  code: "generation_stopped",
                  message: "已停止生成。"
                },
                status: "stopped"
              });
            },
            { once: true }
          );
        })
    );

    renderAssistantShell();

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "帮我整理今天的学习安排" }
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(mockedStreamAssistantConversation).toHaveBeenCalledTimes(1);
    });

    fireEvent.click(screen.getByRole("button", { name: "新对话" }));

    await waitFor(() => {
      expect(screen.getByText("有什么可以帮到你？")).toBeInTheDocument();
    });
    expect(screen.queryByText("已停止生成。")).not.toBeInTheDocument();
  });

  it("ignores stale stream events after resetting to a new draft", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);
    let resolveStream: ((value: { status: "completed" }) => void) | null = null;
    let onEvent: ((event: any) => void) | null = null;

    mockedStreamAssistantConversation.mockImplementation(async (_message, options = {}) => {
      onEvent = options.onEvent || null;
      return new Promise((resolve) => {
        resolveStream = resolve as (value: { status: "completed" }) => void;
      });
    });

    renderAssistantShell();

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "请帮我梳理学生守则第二章" }
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(mockedStreamAssistantConversation).toHaveBeenCalledTimes(1);
    });

    fireEvent.click(screen.getByRole("button", { name: "新对话" }));

    await act(async () => {
      onEvent?.({
        session: {
          created_at: "2026-04-10T10:30:00Z",
          id: "session-created",
          last_message_at: "2026-04-10T10:30:00Z",
          title: "请帮我梳理学生守则第二章",
          updated_at: "2026-04-10T10:30:00Z"
        },
        type: "session_created"
      });
      onEvent?.({
        message: {
          created_at: "2026-04-10T10:30:01Z",
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
          sequence_no: 2
        },
        type: "task_suggestion"
      });
      onEvent?.({
        delta: "旧 turn 的流式内容",
        type: "message_delta"
      });
      resolveStream?.({ status: "completed" });
    });

    await waitFor(() => {
      expect(screen.getByText("有什么可以帮到你？")).toBeInTheDocument();
    });
    expect(screen.queryByText("建议创建任务")).not.toBeInTheDocument();
    expect(screen.queryByText("旧 turn 的流式内容")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "打开会话 请帮我梳理学生守则第二章" })).not.toBeInTheDocument();
  });
});
