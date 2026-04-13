import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { AssistantShell } from "./assistant-shell";
import { AppChrome } from "../app-chrome";
import {
  confirmAssistantTaskSuggestion,
  deleteAssistantSession,
  getAssistantSession,
  getAssistantSessions,
  streamAssistantConversation,
  streamAssistantMessage,
  toAssistantTurnError,
  uploadAssistantFile
} from "../../lib/api/assistant";

vi.mock("../../lib/api/assistant", () => ({
  confirmAssistantTaskSuggestion: vi.fn(),
  deleteAssistantSession: vi.fn(),
  getAssistantSession: vi.fn(),
  getAssistantSessions: vi.fn(),
  streamAssistantConversation: vi.fn(),
  streamAssistantMessage: vi.fn(),
  toAssistantTurnError: vi.fn((error) => error),
  uploadAssistantFile: vi.fn()
}));

vi.mock("next/navigation", () => ({
  usePathname: () => "/"
}));

const mockedGetAssistantSessions = vi.mocked(getAssistantSessions);
const mockedGetAssistantSession = vi.mocked(getAssistantSession);
const mockedStreamAssistantConversation = vi.mocked(streamAssistantConversation);
const mockedStreamAssistantMessage = vi.mocked(streamAssistantMessage);
const mockedToAssistantTurnError = vi.mocked(toAssistantTurnError);
const mockedUploadAssistantFile = vi.mocked(uploadAssistantFile);
const mockedConfirmAssistantTaskSuggestion = vi.mocked(confirmAssistantTaskSuggestion);
const mockedDeleteAssistantSession = vi.mocked(deleteAssistantSession);

function renderAssistantShell() {
  return render(
    <AppChrome>
      <AssistantShell />
    </AppChrome>
  );
}

describe("AssistantShell", () => {
  beforeEach(() => {
    mockedGetAssistantSessions.mockReset();
    mockedGetAssistantSession.mockReset();
    mockedStreamAssistantConversation.mockReset();
    mockedStreamAssistantMessage.mockReset();
    mockedToAssistantTurnError.mockClear();
    mockedUploadAssistantFile.mockReset();
    mockedConfirmAssistantTaskSuggestion.mockReset();
    mockedDeleteAssistantSession.mockReset();
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

    fireEvent.change(screen.getByLabelText("输入消息"), {
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

    expect(screen.getByText("昨天那条历史消息")).toBeInTheDocument();
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

    fireEvent.change(screen.getByLabelText("输入消息"), {
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
});
