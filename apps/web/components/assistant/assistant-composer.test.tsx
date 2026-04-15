import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { AssistantComposer } from "./assistant-composer";

describe("AssistantComposer", () => {
  it("limits file input types and shows supported format hint", () => {
    const { container } = render(
      <AssistantComposer
        canUpload
        onSubmitMessage={() => {}}
        onUploadFile={() => {}}
        uploadAccept=".md,.txt"
        uploadHint="支持 md、txt"
      />
    );

    const input = container.querySelector('input[type="file"]');
    if (!input) {
      throw new Error("expected file input to exist");
    }

    expect(input).toHaveAttribute("accept", ".md,.txt");
    expect(screen.getByText("支持 md、txt")).toBeInTheDocument();
  });

  it("forwards the selected file to upload handler", () => {
    const handleUploadFile = vi.fn();
    const { container } = render(
      <AssistantComposer
        canUpload
        onSubmitMessage={() => {}}
        onUploadFile={handleUploadFile}
        uploadAccept=".md,.txt"
        uploadHint="支持 md、txt"
      />
    );

    const input = container.querySelector('input[type="file"]') as HTMLInputElement | null;
    if (!input) {
      throw new Error("expected file input to exist");
    }

    const file = new File(["# 学生守则"], "学生守则.md", { type: "text/markdown" });
    fireEvent.change(input, { target: { files: [file] } });

    expect(handleUploadFile).toHaveBeenCalledWith(file);
  });

  it("caps auto-resize at 250px and enables internal scrolling for long input", async () => {
    render(
      <AssistantComposer
        canUpload
        onSubmitMessage={vi.fn()}
        onUploadFile={vi.fn()}
        uploadAccept=".md,.txt"
        uploadHint="支持 md、txt"
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
