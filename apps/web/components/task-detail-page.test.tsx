import React from "react";
import { render, screen, waitFor } from "@testing-library/react";

import TaskDetailPage from "../app/tasks/[id]/page";
import { getTask, getTaskArtifacts, getTaskEvents } from "../lib/api/tasks";

vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "task-1" })
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
  beforeEach(() => {
    mockedGetTask.mockReset();
    mockedGetTaskArtifacts.mockReset();
    mockedGetTaskEvents.mockReset();
  });

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

    expect(await screen.findByRole("link", { name: "查看修订结果" })).toHaveAttribute(
      "href",
      "/resources/resource-1"
    );
    expect(screen.getByRole("link", { name: "下载修订结果" })).toHaveAttribute(
      "href",
      "/api/resources/resource-1/export"
    );
  });
});
