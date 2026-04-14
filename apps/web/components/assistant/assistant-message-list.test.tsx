import { render, screen } from "@testing-library/react";

import type { AssistantMessage } from "../../lib/assistant/types";
import { AssistantMessageList } from "./assistant-message-list";

describe("AssistantMessageList", () => {
  const originalEnv = process.env;

  beforeEach(() => {
    process.env = { ...originalEnv, NEXT_PUBLIC_API_URL: "http://127.0.0.1:18080" };
  });

  afterEach(() => {
    process.env = originalEnv;
  });

  it("shows original file download link when session file has file_id", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          {
            created_at: "2026-04-13T10:00:00Z",
            id: "message-file",
            kind: "session_file",
            payload: {
              file_id: "file-123",
              file_name: "学生守则.pdf",
              resource_id: "resource-1",
              resource_title: "学生守则",
              source_type: "upload",
              status: "ready"
            },
            role: "assistant",
            sequence_no: 1
          } satisfies AssistantMessage
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.getByRole("link", { name: "下载原文件" })).toHaveAttribute(
      "href",
      "http://127.0.0.1:18080/api/files/file-123/download"
    );
  });

  it("does not show original file download link when file_id is missing", () => {
    render(
      <AssistantMessageList
        activeTaskSuggestionId={null}
        messages={[
          {
            created_at: "2026-04-13T10:00:00Z",
            id: "message-file",
            kind: "session_file",
            payload: {
              file_name: "学生守则.pdf",
              resource_id: "resource-1",
              resource_title: "学生守则",
              source_type: "upload",
              status: "ready"
            },
            role: "assistant",
            sequence_no: 1
          } satisfies AssistantMessage
        ]}
        onConfirmTaskSuggestion={() => {}}
      />
    );

    expect(screen.queryByRole("link", { name: "下载原文件" })).not.toBeInTheDocument();
  });
});

