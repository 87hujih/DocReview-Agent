import { render, screen } from "@testing-library/react";

import { ResourceVersionViewer } from "./resource-version-viewer";

describe("ResourceVersionViewer", () => {
  it("renders the current resource version and exposes a markdown export link", () => {
    render(
      <ResourceVersionViewer
        resource={{
          created_at: "2026-04-13T10:00:00Z",
          id: "resource-1",
          source_type: "upload",
          title: "学生守则"
        }}
        version={{
          content: "# 修订结果\n最终内容",
          created_at: "2026-04-13T10:05:00Z",
          id: "version-2",
          source: "task_revision",
          version_number: 2
        }}
      />
    );

    expect(screen.getByRole("heading", { name: "学生守则" })).toBeInTheDocument();
    expect(screen.getByText("版本 2 / task_revision")).toBeInTheDocument();
    expect(
      screen.getByText(
        (_, element) => element?.tagName === "PRE" && element.textContent === "# 修订结果\n最终内容"
      )
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "下载修订结果" })).toHaveAttribute(
      "href",
      "/api/resources/resource-1/export"
    );
  });
});
