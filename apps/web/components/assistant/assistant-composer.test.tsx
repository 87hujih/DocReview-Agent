import { fireEvent, render, screen } from "@testing-library/react";

import { AssistantComposer } from "./assistant-composer";

describe("AssistantComposer", () => {
  it("limits file input types and shows supported format hint", () => {
    const { container } = render(
      <AssistantComposer
        canUpload
        onSubmitMessage={() => {}}
        onUploadFile={() => {}}
      />
    );

    const input = container.querySelector('input[type="file"]');
    if (!input) {
      throw new Error("expected file input to exist");
    }

    expect(input).toHaveAttribute("accept", ".md,.txt,.doc,.docx,.pdf,.rtf,.odt");
    expect(screen.getByText("支持 md、txt、doc、docx、pdf、rtf、odt")).toBeInTheDocument();
  });

  it("forwards the selected file to upload handler", () => {
    const handleUploadFile = vi.fn();
    const { container } = render(
      <AssistantComposer
        canUpload
        onSubmitMessage={() => {}}
        onUploadFile={handleUploadFile}
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
});
