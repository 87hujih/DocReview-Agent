import React from "react";
import { render, screen, waitFor } from "@testing-library/react";

import TaskCreatePageClient from "../app/tasks/new/task-create-client";
import { getResource, searchResource } from "../lib/api/resources";
import { createTask } from "../lib/api/tasks";

const mockedPush = vi.fn();

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: mockedPush
  })
}));

vi.mock("../lib/api/resources", () => ({
  getResource: vi.fn(),
  searchResource: vi.fn()
}));

vi.mock("../lib/api/tasks", () => ({
  createTask: vi.fn()
}));

const mockedGetResource = vi.mocked(getResource);
const mockedSearchResource = vi.mocked(searchResource);
const mockedCreateTask = vi.mocked(createTask);

describe("TaskCreatePageClient", () => {
  beforeEach(() => {
    mockedPush.mockReset();
    mockedGetResource.mockReset();
    mockedSearchResource.mockReset();
    mockedCreateTask.mockReset();
  });

  it("keeps the editable panels hidden while the selected resource is still loading", () => {
    mockedGetResource.mockReturnValue(new Promise(() => undefined));

    render(<TaskCreatePageClient resourceId="resource-1" />);

    expect(screen.getByText("正在加载资源上下文")).toBeInTheDocument();
    expect(screen.queryByLabelText("修订指令")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "提交任务" })).not.toBeInTheDocument();
    expect(screen.queryByText("未选择")).not.toBeInTheDocument();
  });

  it("keeps the task form hidden when the selected resource fails to load", async () => {
    mockedGetResource.mockRejectedValue(new Error("资源详情加载失败"));

    render(<TaskCreatePageClient resourceId="resource-1" />);

    expect(await screen.findByText("错误 > 资源详情加载失败")).toBeInTheDocument();
    expect(screen.queryByLabelText("修订指令")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "检索引用" })).not.toBeInTheDocument();
  });

  it("shows the task form after the selected resource loads", async () => {
    mockedGetResource.mockResolvedValue({
      current_version: {
        content: "# handbook",
        created_at: "2026-04-12T09:00:00Z",
        id: "version-1",
        source: "upload",
        version_number: 3
      },
      resource: {
        created_at: "2026-04-11T18:30:00Z",
        id: "resource-1",
        source_type: "markdown",
        title: "员工手册"
      }
    });

    render(<TaskCreatePageClient resourceId="resource-1" />);

    await waitFor(() => {
      expect(mockedGetResource).toHaveBeenCalledWith("resource-1");
    });

    expect(await screen.findByLabelText("修订指令")).toBeInTheDocument();
    expect(screen.getAllByText("员工手册").length).toBeGreaterThan(0);
    expect(screen.getByText("版本 3 / upload")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交任务" })).toBeInTheDocument();
  });
});
