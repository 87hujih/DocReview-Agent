import { render, screen, waitFor } from "@testing-library/react";

import { AssistantShell } from "./assistant-shell";
import { AppChrome } from "../app-chrome";
import { getAssistantSessions } from "../../lib/api/assistant";

vi.mock("next/navigation", () => ({
  usePathname: () => "/"
}));

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

const mockedGetAssistantSessions = vi.mocked(getAssistantSessions);

describe("assistant sidebar layout", () => {
  beforeEach(() => {
    mockedGetAssistantSessions.mockReset();
  });

  it("shows assistant history in the global sidebar without rendering a second sidebar separator", async () => {
    mockedGetAssistantSessions.mockResolvedValue([
      {
        created_at: "2026-04-10T10:00:00Z",
        id: "session-1",
        last_message_at: "2026-04-10T10:30:00Z",
        title: "昨天整理学生守则的思路",
        updated_at: "2026-04-10T10:30:00Z"
      }
    ]);

    render(
      <AppChrome>
        <AssistantShell />
      </AppChrome>
    );

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "打开会话 昨天整理学生守则的思路" })).toBeInTheDocument();
    });

    expect(screen.queryByRole("separator")).not.toBeInTheDocument();
  });
});
