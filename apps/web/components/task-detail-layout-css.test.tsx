import { readFileSync } from "node:fs";
import { join } from "node:path";

describe("task detail layout css", () => {
  it("keeps the task detail page scrollable inside the fixed app viewport", () => {
    const pageCss = readFileSync(
      join(process.cwd(), "app", "tasks", "[id]", "page.module.css"),
      "utf8"
    );

    expect(pageCss).toContain("display: flex;");
    expect(pageCss).toContain("flex-direction: column;");
    expect(pageCss).toContain("align-items: stretch;");
    expect(pageCss).toContain(".page > *");
    expect(pageCss).toContain("flex: 0 0 auto;");
    expect(pageCss).toContain("height: 100%;");
    expect(pageCss).toContain("min-height: 0;");
    expect(pageCss).toContain("width: min(100%, 1120px);");
    expect(pageCss).toContain("margin: 0 auto;");
    expect(pageCss).toContain("overflow-y: auto;");
    expect(pageCss).toContain("scrollbar-width: none;");
  });

  it("prevents generated timeline and diff text from forcing horizontal overflow", () => {
    const timelineCss = readFileSync(join(process.cwd(), "components", "task-timeline.module.css"), "utf8");
    const diffPaneCss = readFileSync(join(process.cwd(), "components", "ui", "diff-pane.module.css"), "utf8");

    expect(timelineCss).toContain("min-width: 0;");
    expect(timelineCss).toContain("overflow-wrap: anywhere;");
    expect(diffPaneCss).toContain("min-width: 0;");
    expect(diffPaneCss).toContain("overflow-wrap: anywhere;");
  });
});
