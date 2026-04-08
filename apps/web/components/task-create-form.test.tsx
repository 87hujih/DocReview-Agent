import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { TaskCreateForm } from "./task-create-form";

describe("TaskCreateForm", () => {
  it("renders textarea and submit button, accepts input, and submits the instruction", async () => {
    const handleSubmit = vi.fn();

    render(
      <TaskCreateForm
        resource={{
          createdAt: "2026-04-08T09:35:12Z",
          id: "res-001",
          sourceType: "markdown",
          title: "Employee Handbook"
        }}
        onSubmit={handleSubmit}
      />
    );

    const textarea = screen.getByLabelText("INSTRUCTION");

    fireEvent.change(textarea, {
      target: { value: "请把风险提示段落改得更明确。" }
    });

    expect(textarea).toHaveValue("请把风险提示段落改得更明确。");

    fireEvent.click(screen.getByRole("button", { name: "SUBMIT_TASK" }));

    await waitFor(() => {
      expect(handleSubmit).toHaveBeenCalledWith("请把风险提示段落改得更明确。");
    });
  });
});
