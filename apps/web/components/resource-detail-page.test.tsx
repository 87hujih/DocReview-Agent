import React from "react";
import { render, screen, waitFor } from "@testing-library/react";

import ResourceDetailPage from "../app/resources/[id]/page";
import { getResource } from "../lib/api/resources";

let currentSessionId: string | null = null;

vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "resource-1" }),
  useSearchParams: () => ({
    get: (key: string) => (key === "session" ? currentSessionId : null)
  })
}));

vi.mock("../lib/api/resources", async () => {
  const actual = await vi.importActual<typeof import("../lib/api/resources")>("../lib/api/resources");
  return {
    ...actual,
    getResource: vi.fn()
  };
});

const mockedGetResource = vi.mocked(getResource);

describe("ResourceDetailPage", () => {
  beforeEach(() => {
    currentSessionId = null;
    mockedGetResource.mockReset();
  });

  it("loads and renders the current version viewer", async () => {
    mockedGetResource.mockResolvedValue({
      current_version: {
        content: "最终修订正文",
        created_at: "2026-04-13T10:05:00Z",
        id: "version-2",
        source: "task_revision",
        version_number: 2
      },
      resource: {
        created_at: "2026-04-13T10:00:00Z",
        id: "resource-1",
        source_type: "upload",
        title: "学生守则"
      }
    });

    render(<ResourceDetailPage />);

    await waitFor(() => {
      expect(mockedGetResource).toHaveBeenCalledWith("resource-1");
    });

    expect(await screen.findByRole("heading", { name: "学生守则" })).toBeInTheDocument();
    expect(screen.getByText("最终修订正文")).toBeInTheDocument();
  });

  it("shows a return-to-session link when opened from an assistant session", async () => {
    currentSessionId = "session-1";
    mockedGetResource.mockResolvedValue({
      current_version: {
        content: "最终修订正文",
        created_at: "2026-04-13T10:05:00Z",
        id: "version-2",
        source: "task_revision",
        version_number: 2
      },
      resource: {
        created_at: "2026-04-13T10:00:00Z",
        id: "resource-1",
        source_type: "upload",
        title: "学生守则"
      }
    });

    render(<ResourceDetailPage />);

    expect(await screen.findByRole("link", { name: "返回原会话" })).toHaveAttribute(
      "href",
      "/?session=session-1"
    );
  });
});
