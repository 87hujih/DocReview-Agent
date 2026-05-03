import React from "react";
import { act, render, screen, waitFor } from "@testing-library/react";

import TaskDetailPage from "../app/tasks/[id]/page";
import { getTask, getTaskArtifacts, getTaskEvents } from "../lib/api/tasks";

let currentSessionId: string | null = null;

vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "task-1" }),
  useSearchParams: () => ({
    get: (key: string) => (key === "session" ? currentSessionId : null)
  })
}));

vi.mock("../lib/api/tasks", async () => {
  const actual = await vi.importActual<typeof import("../lib/api/tasks")>("../lib/api/tasks");
  return {
    ...actual,
    getTask: vi.fn(),
    getTaskArtifacts: vi.fn(),
    getTaskEvents: vi.fn()
  };
});

const mockedGetTask = vi.mocked(getTask);
const mockedGetTaskArtifacts = vi.mocked(getTaskArtifacts);
const mockedGetTaskEvents = vi.mocked(getTaskEvents);

describe("TaskDetailPage", () => {
  const originalEnv = process.env;
  const pendingTaskResponse = {
    steps: [
      {
        completed_at: undefined,
        id: "step-1",
        started_at: "2026-04-13T10:00:05Z",
        status: "running",
        step_name: "retriever"
      }
    ],
    task: {
      created_at: "2026-04-13T10:00:00Z",
      id: "task-1",
      instruction: "修订学生守则",
      resource_id: "resource-1",
      status: "pending",
      updated_at: "2026-04-13T10:00:10Z"
    }
  };

  beforeEach(() => {
    process.env = { ...originalEnv };
    process.env.NEXT_PUBLIC_API_URL = "http://127.0.0.1:18080";
    currentSessionId = null;
    mockedGetTask.mockReset();
    mockedGetTaskArtifacts.mockReset();
    mockedGetTaskEvents.mockReset();
  });

  afterEach(() => {
    process.env = originalEnv;
    vi.useRealTimers();
  });

  async function flushMicrotasks() {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  }

  it("shows result view and export links when the task is completed", async () => {
    mockedGetTask.mockResolvedValue({
      steps: [],
      task: {
        created_at: "2026-04-13T10:00:00Z",
        id: "task-1",
        instruction: "修订学生守则",
        resource_id: "resource-1",
        status: "completed",
        updated_at: "2026-04-13T10:20:00Z"
      }
    });
    mockedGetTaskArtifacts.mockResolvedValue([]);
    mockedGetTaskEvents.mockResolvedValue([]);

    render(<TaskDetailPage />);

    await waitFor(() => {
      expect(mockedGetTask).toHaveBeenCalledWith("task-1");
    });

    expect(
      await screen.findByText("任务流程已执行完成，可以返回助手继续下一步，或查看修订结果。")
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "返回助手" })).toHaveAttribute("href", "/");
    expect(await screen.findByRole("link", { name: "查看修订结果" })).toHaveAttribute(
      "href",
      "/resources/resource-1"
    );
    expect(screen.getByRole("link", { name: "下载修订结果" })).toHaveAttribute(
      "href",
      "http://127.0.0.1:18080/api/resources/resource-1/export"
    );
    expect(screen.getByText("已完成")).toBeInTheDocument();
    expect(screen.queryByText("completed")).not.toBeInTheDocument();
    expect(screen.queryByText("正在等待步骤事件")).not.toBeInTheDocument();
    expect(screen.queryByText("等待产物生成")).not.toBeInTheDocument();
  });

  it("shows return-to-session links when the page is opened from an assistant session", async () => {
    currentSessionId = "session-1";
    mockedGetTask.mockResolvedValue({
      steps: [],
      task: {
        created_at: "2026-04-13T10:00:00Z",
        id: "task-1",
        instruction: "修订学生守则",
        resource_id: "resource-1",
        status: "completed",
        updated_at: "2026-04-13T10:20:00Z"
      }
    });
    mockedGetTaskArtifacts.mockResolvedValue([]);
    mockedGetTaskEvents.mockResolvedValue([]);

    render(<TaskDetailPage />);

    expect(await screen.findByRole("link", { name: "返回原会话" })).toHaveAttribute(
      "href",
      "/?session=session-1"
    );
    expect(screen.getByRole("link", { name: "查看修订结果" })).toHaveAttribute(
      "href",
      "/resources/resource-1?session=session-1"
    );
  });

  it("falls back to the task source session when the URL does not carry a session query", async () => {
    mockedGetTask.mockResolvedValue({
      steps: [],
      task: {
        created_at: "2026-04-13T10:00:00Z",
        id: "task-1",
        instruction: "修订学生守则",
        resource_id: "resource-1",
        source_session_id: "session-origin",
        status: "completed",
        updated_at: "2026-04-13T10:20:00Z"
      }
    });
    mockedGetTaskArtifacts.mockResolvedValue([]);
    mockedGetTaskEvents.mockResolvedValue([]);

    render(<TaskDetailPage />);

    expect(await screen.findByRole("link", { name: "返回原会话" })).toHaveAttribute(
      "href",
      "/?session=session-origin"
    );
    expect(screen.getByRole("link", { name: "查看修订结果" })).toHaveAttribute(
      "href",
      "/resources/resource-1?session=session-origin"
    );
  });

  it("shows a pure error state when the initial task load fails", async () => {
    mockedGetTask.mockRejectedValue(new Error("任务不存在"));
    mockedGetTaskArtifacts.mockResolvedValue([]);
    mockedGetTaskEvents.mockResolvedValue([]);

    render(<TaskDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("错误 > 任务不存在")).toBeInTheDocument();
    });

    expect(screen.queryByText("正在等待步骤事件")).not.toBeInTheDocument();
    expect(screen.queryByText("等待产物生成")).not.toBeInTheDocument();
    expect(screen.queryByText("未知")).not.toBeInTheDocument();
  });

  it("keeps task meta visible when artifacts fail on initial load", async () => {
    mockedGetTask.mockResolvedValue(pendingTaskResponse);
    mockedGetTaskArtifacts.mockRejectedValue(new Error("产物暂时不可用"));
    mockedGetTaskEvents.mockResolvedValue([]);

    render(<TaskDetailPage />);

    expect(await screen.findByText("任务状态")).toBeInTheDocument();
    expect(screen.getByText("检索器")).toBeInTheDocument();
    expect(screen.queryByText("等待产物生成")).not.toBeInTheDocument();
  });

  it("keeps step summary visible when events fail on initial load", async () => {
    mockedGetTask.mockResolvedValue(pendingTaskResponse);
    mockedGetTaskArtifacts.mockResolvedValue([]);
    mockedGetTaskEvents.mockRejectedValue(new Error("事件流暂时不可用"));

    render(<TaskDetailPage />);

    expect(await screen.findByText("任务状态")).toBeInTheDocument();
    expect(screen.getByText("检索器")).toBeInTheDocument();
    expect(screen.getByText("步骤状态 运行中")).toBeInTheDocument();
  });

  it("keeps polling after an initial artifacts failure so artifacts can recover later", async () => {
    vi.useFakeTimers();
    mockedGetTask.mockResolvedValue(pendingTaskResponse);
    mockedGetTaskArtifacts
      .mockRejectedValueOnce(new Error("产物暂时不可用"))
      .mockResolvedValueOnce([
        {
          artifact_type: "citations",
          content: [
            {
              citation_id: "cite-1",
              resource_id: "resource-1",
              section_title: "审批流程",
              snippet: "后续轮询已恢复引用产物"
            }
          ],
          created_at: "2026-04-13T10:00:20Z",
          id: "artifact-1"
        }
      ]);
    mockedGetTaskEvents.mockResolvedValue([]);

    render(<TaskDetailPage />);

    await act(async () => {
      await flushMicrotasks();
    });

    expect(screen.getByText("任务状态")).toBeInTheDocument();
    expect(mockedGetTaskArtifacts).toHaveBeenCalledTimes(1);

    await act(async () => {
      vi.advanceTimersByTime(3000);
      await flushMicrotasks();
    });

    expect(screen.getByText("后续轮询已恢复引用产物")).toBeInTheDocument();
    expect(mockedGetTaskArtifacts).toHaveBeenCalledTimes(2);
  });

  it("does not start a second poll while the previous poll is still in flight", async () => {
    vi.useFakeTimers();

    let resolveTaskPoll: ((value: typeof pendingTaskResponse) => void) | null = null;
    const pendingTaskPoll = new Promise<typeof pendingTaskResponse>((resolve) => {
      resolveTaskPoll = resolve;
    });

    mockedGetTask
      .mockResolvedValueOnce(pendingTaskResponse)
      .mockImplementationOnce(() => pendingTaskPoll)
      .mockResolvedValue(pendingTaskResponse);
    mockedGetTaskArtifacts.mockResolvedValue([]);
    mockedGetTaskEvents.mockResolvedValue([]);

    render(<TaskDetailPage />);

    await act(async () => {
      await flushMicrotasks();
    });

    expect(screen.getByText("任务状态")).toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(3000);
      await flushMicrotasks();
    });

    expect(mockedGetTask).toHaveBeenCalledTimes(2);

    await act(async () => {
      vi.advanceTimersByTime(3000);
      await flushMicrotasks();
    });

    expect(mockedGetTask).toHaveBeenCalledTimes(2);

    resolveTaskPoll?.(pendingTaskResponse);

    await act(async () => {
      await pendingTaskPoll;
    });
  });
});
