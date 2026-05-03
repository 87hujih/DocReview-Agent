import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AssistantMarkdown } from "./assistant-markdown";

describe("AssistantMarkdown", () => {
  it("renders headings paragraphs and lists", () => {
    render(<AssistantMarkdown content={"# 标题\n\n第一段。\n\n- 项目一\n- 项目二"} />);

    expect(screen.getByRole("heading", { level: 1, name: "标题" })).toBeInTheDocument();
    expect(screen.getByText("第一段。")).toBeInTheDocument();
    expect(screen.getByText("项目一").closest("li")).not.toBeNull();
    expect(screen.getByText("项目二").closest("li")).not.toBeNull();
  });

  it("renders blockquote inline code and fenced code block", () => {
    render(
      <AssistantMarkdown content={"> 引用内容\n\n这里有 `命令`。\n\n```bash\nnpm test\n```"} />
    );

    expect(screen.getByText("引用内容").closest("blockquote")).not.toBeNull();
    expect(screen.getByText("命令", { selector: "code" })).toBeInTheDocument();
    expect(screen.getByText("npm test", { selector: "code" }).closest("pre")).not.toBeNull();
  });

  it("renders safe links and downgrades unsafe links to plain text", () => {
    render(
      <AssistantMarkdown
        content={"[安全链接](https://example.com)\n\n[危险链接](javascript:alert('xss'))"}
      />
    );

    expect(screen.getByRole("link", { name: "安全链接" })).toHaveAttribute("href", "https://example.com");
    expect(screen.queryByRole("link", { name: "危险链接" })).not.toBeInTheDocument();
    expect(screen.getByText(/^危险链接$/)).toBeInTheDocument();
  });

  it("does not parse raw html", () => {
    const { container } = render(<AssistantMarkdown content={"普通文本 <b>粗体</b>"} />);

    expect(container.querySelector("b")).toBeNull();
    expect(screen.getByText("普通文本 <b>粗体</b>")).toBeInTheDocument();
  });

  it("renders an unfinished fenced code block at the end of streaming content", () => {
    render(<AssistantMarkdown content={"```ts\nconst value = 1;"} />);

    expect(screen.getByText("const value = 1;", { selector: "code" }).closest("pre")).not.toBeNull();
  });

  it("keeps unmatched inline markdown markers literal while streaming", () => {
    render(<AssistantMarkdown content={"这里有 **未闭合强调"} />);

    expect(screen.getByText("这里有 **未闭合强调")).toBeInTheDocument();
    expect(screen.queryByText("未闭合强调", { selector: "strong" })).not.toBeInTheDocument();
  });
});
