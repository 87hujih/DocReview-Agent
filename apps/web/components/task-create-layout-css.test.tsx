import { readFileSync } from "node:fs";
import { join } from "node:path";

describe("task create layout css", () => {
  it("locks the task create page into the main viewport and lets the page scroll internally", () => {
    const pageCss = readFileSync(
      join(process.cwd(), "app", "tasks", "new", "page.module.css"),
      "utf8"
    );

    expect(pageCss).toContain("display: flex;");
    expect(pageCss).toContain("flex-direction: column;");
    expect(pageCss).toContain(".page > *");
    expect(pageCss).toContain("flex: 0 0 auto;");
    expect(pageCss).toContain("align-items: stretch;");
    expect(pageCss).toContain("height: 100%;");
    expect(pageCss).toContain("min-height: 0;");
    expect(pageCss).toContain("width: min(100%, 1120px);");
    expect(pageCss).toContain("margin: 0 auto;");
    expect(pageCss).toContain("overflow-y: auto;");
    expect(pageCss).toContain("scrollbar-width: none;");
  });

  it("promotes the primary actions instead of leaving them as low-contrast corner buttons", () => {
    const formCss = readFileSync(join(process.cwd(), "components", "task-create-form.module.css"), "utf8");
    const searchCss = readFileSync(join(process.cwd(), "components", "resource-search.module.css"), "utf8");

    expect(formCss).toContain("justify-self: end;");
    expect(formCss).toContain("min-width: 140px;");
    expect(formCss).toContain("border-color: var(--terminal-info);");
    expect(searchCss).toContain("justify-self: end;");
    expect(searchCss).toContain("min-width: 140px;");
    expect(searchCss).toContain("border-color: var(--terminal-info);");
    expect(formCss).toContain("@media (max-width: 640px)");
    expect(formCss).toContain("width: 100%;");
    expect(searchCss).toContain("@media (max-width: 640px)");
    expect(searchCss).toContain("width: 100%;");
  });

  it("bounds the instruction textarea so it cannot resize over the citation search panel", () => {
    const formCss = readFileSync(join(process.cwd(), "components", "task-create-form.module.css"), "utf8");

    expect(formCss).toContain("min-height: 180px;");
    expect(formCss).toContain("max-height: 320px;");
    expect(formCss).toContain("overflow-y: auto;");
    expect(formCss).toContain("resize: none;");
  });
});
