import React from "react";
import { render, screen, waitFor } from "@testing-library/react";

import ResourcesPage from "../app/resources/page";
import { getResources } from "../lib/api/resources";

vi.mock("../lib/api/resources", () => ({
  getResources: vi.fn()
}));

const mockedGetResources = vi.mocked(getResources);

describe("ResourcesPage", () => {
  beforeEach(() => {
    mockedGetResources.mockReset();
  });

  it("does not expose the api debug banner after loading resources", async () => {
    mockedGetResources.mockResolvedValue([
      {
        created_at: "2026-04-10T19:59:07.138641+08:00",
        id: "resource-1",
        source_type: "upload",
        title: "示例资源"
      }
    ]);

    render(<ResourcesPage />);

    await waitFor(() => {
      expect(mockedGetResources).toHaveBeenCalledTimes(1);
    });

    expect(await screen.findByText("示例资源")).toBeInTheDocument();
    expect(screen.queryByText("输出 > 正在调用 /api/resources")).not.toBeInTheDocument();
  });

  it("shows a real error state instead of pretending the list is empty", async () => {
    mockedGetResources.mockRejectedValue(new Error("资源接口失败"));

    render(<ResourcesPage />);

    expect(await screen.findByText("错误 > 资源接口失败")).toBeInTheDocument();
    expect(screen.queryByText("当前没有可用资源")).not.toBeInTheDocument();
    expect(screen.queryByText(/^资源数量 0$/)).not.toBeInTheDocument();
  });
});
