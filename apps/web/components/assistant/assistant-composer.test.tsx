import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { AssistantComposer } from "./assistant-composer";

describe("AssistantComposer", () => {
  it("caps auto-resize at 250px and enables internal scrolling for long input", async () => {
    render(
      <AssistantComposer
        canUpload
        onSubmitMessage={vi.fn()}
        onUploadFile={vi.fn()}
      />
    );

    const textarea = screen.getByLabelText("输入消息") as HTMLTextAreaElement;
    const sendButton = screen.getByRole("button", { name: "发送" });

    expect(sendButton).toBeDisabled();

    Object.defineProperty(textarea, "scrollHeight", {
      configurable: true,
      value: 320
    });

    fireEvent.change(textarea, {
      target: {
        value: "这是一段很长的输入内容，用来验证输入框的自动增高上限是否会被限制在 250px。"
      }
    });

    await waitFor(() => {
      expect(textarea.style.height).toBe("250px");
      expect(textarea.style.overflowY).toBe("auto");
    });

    expect(sendButton).not.toBeDisabled();
  });
});
