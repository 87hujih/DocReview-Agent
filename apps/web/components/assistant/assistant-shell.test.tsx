import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { AssistantShell } from "./assistant-shell";
import {
  appendAssistantMessage,
  confirmAssistantTaskSuggestion,
  createAssistantConversation,
  deleteAssistantSession,
  getAssistantSession,
  getAssistantSessions,
  uploadAssistantFile
} from "../../lib/api/assistant";

vi.mock("../../lib/api/assistant", () => ({
  appendAssistantMessage: vi.fn(),
  confirmAssistantTaskSuggestion: vi.fn(),
  createAssistantConversation: vi.fn(),
  deleteAssistantSession: vi.fn(),
  getAssistantSession: vi.fn(),
  getAssistantSessions: vi.fn(),
  uploadAssistantFile: vi.fn()
}));

const mockedGetAssistantSessions = vi.mocked(getAssistantSessions);
const mockedGetAssistantSession = vi.mocked(getAssistantSession);
const mockedCreateAssistantConversation = vi.mocked(createAssistantConversation);
const mockedAppendAssistantMessage = vi.mocked(appendAssistantMessage);
const mockedUploadAssistantFile = vi.mocked(uploadAssistantFile);
const mockedConfirmAssistantTaskSuggestion = vi.mocked(confirmAssistantTaskSuggestion);
const mockedDeleteAssistantSession = vi.mocked(deleteAssistantSession);

describe("AssistantShell", () => {
  beforeEach(() => {
    mockedGetAssistantSessions.mockReset();
    mockedGetAssistantSession.mockReset();
    mockedCreateAssistantConversation.mockReset();
    mockedAppendAssistantMessage.mockReset();
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

    render(<AssistantShell />);

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
    });

    expect(screen.getByRole("button", { name: "新对话" })).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText("昨天整理学生守则的思路")).toBeInTheDocument();
    });
    expect(screen.getByText("发送第一条消息后才会真正创建会话。")).toBeInTheDocument();
  });

  it("creates a real session from the draft when the first message is sent", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);
    mockedCreateAssistantConversation.mockResolvedValue({
      messages: [
        {
          created_at: "2026-04-10T10:30:00Z",
          id: "message-1",
          kind: "text",
          payload: {
            content: "请帮我梳理学生守则第二章"
          },
          role: "user",
          sequence_no: 1
        },
        {
          created_at: "2026-04-10T10:30:01Z",
          id: "message-2",
          kind: "text",
          payload: {
            content: "我先继续和你梳理问题，需要时再一起收敛成任务。"
          },
          role: "assistant",
          sequence_no: 2
        }
      ],
      session: {
        created_at: "2026-04-10T10:30:00Z",
        id: "session-created",
        last_message_at: "2026-04-10T10:30:01Z",
        title: "请帮我梳理学生守则第二章",
        updated_at: "2026-04-10T10:30:01Z"
      }
    });

    render(<AssistantShell />);

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByLabelText("输入消息"), {
      target: { value: "请帮我梳理学生守则第二章" }
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(mockedCreateAssistantConversation).toHaveBeenCalledWith("请帮我梳理学生守则第二章");
    });

    await waitFor(() => {
      expect(screen.getAllByText("请帮我梳理学生守则第二章").length).toBeGreaterThan(0);
      expect(screen.getByText("我先继续和你梳理问题，需要时再一起收敛成任务。")).toBeInTheDocument();
    });
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

    render(<AssistantShell />);

    const historyButton = await screen.findByRole("button", { name: "打开会话 昨天整理学生守则的思路" });
    fireEvent.click(historyButton);

    await waitFor(() => {
      expect(mockedGetAssistantSession).toHaveBeenCalledWith("session-1");
    });

    expect(screen.getByText("昨天那条历史消息")).toBeInTheDocument();
  });

  it("shows a pending assistant bubble inside the conversation while creating the first session", async () => {
    mockedGetAssistantSessions.mockResolvedValue([]);

    let resolveConversation:
      | ((value: {
          messages: Array<{
            created_at: string;
            id: string;
            kind: "text";
            payload: { content: string };
            role: "assistant" | "user";
            sequence_no: number;
          }>;
          session: {
            created_at: string;
            id: string;
            last_message_at: string;
            title: string;
            updated_at: string;
          };
        }) => void)
      | null = null;

    mockedCreateAssistantConversation.mockReturnValue(
      new Promise((resolve) => {
        resolveConversation = resolve;
      })
    );

    render(<AssistantShell />);

    await waitFor(() => {
      expect(mockedGetAssistantSessions).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByLabelText("输入消息"), {
      target: { value: "帮我整理今天的学习安排" }
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(mockedCreateAssistantConversation).toHaveBeenCalledWith("帮我整理今天的学习安排");
    });

    expect(screen.getByText("助手处理中")).toBeInTheDocument();
    expect(screen.queryByText("发送第一条消息后才会真正创建会话。")).not.toBeInTheDocument();

    resolveConversation?.({
      messages: [
        {
          created_at: "2026-04-10T10:30:00Z",
          id: "message-1",
          kind: "text",
          payload: {
            content: "帮我整理今天的学习安排"
          },
          role: "user",
          sequence_no: 1
        },
        {
          created_at: "2026-04-10T10:30:01Z",
          id: "message-2",
          kind: "text",
          payload: {
            content: "我先继续和你梳理问题，需要时再一起收敛成任务。"
          },
          role: "assistant",
          sequence_no: 2
        }
      ],
      session: {
        created_at: "2026-04-10T10:30:00Z",
        id: "session-created",
        last_message_at: "2026-04-10T10:30:01Z",
        title: "帮我整理今天的学习安排",
        updated_at: "2026-04-10T10:30:01Z"
      }
    });

    await waitFor(() => {
      expect(screen.queryByText("助手处理中")).not.toBeInTheDocument();
    });
  });
});
