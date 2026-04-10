import { readFileSync } from "node:fs";
import { join } from "node:path";

describe("assistant layout css", () => {
  it("locks the page viewport and hides internal scrollbars", () => {
    const globalCss = readFileSync(join(process.cwd(), "app", "globals.css"), "utf8");
    const navCss = readFileSync(join(process.cwd(), "components", "nav.module.css"), "utf8");
    const pageCss = readFileSync(join(process.cwd(), "app", "page.module.css"), "utf8");
    const shellCss = readFileSync(join(process.cwd(), "components", "assistant", "assistant-shell.module.css"), "utf8");
    const historyCss = readFileSync(join(process.cwd(), "components", "assistant", "session-history.module.css"), "utf8");

    expect(globalCss).toContain("height: 100%;");
    expect(globalCss).toContain("height: 100dvh;");
    expect(globalCss).toContain("overflow: hidden;");
    expect(globalCss).toContain("display: grid;");
    expect(globalCss).toContain("grid-template-rows: auto minmax(0, 1fr);");
    expect(globalCss).toContain(".app-shell");
    expect(globalCss).toContain("height: 100%;");
    expect(globalCss).toContain("min-height: 0;");
    expect(pageCss).toContain("overflow: hidden;");
    expect(pageCss).toContain("height: 100%;");
    expect(shellCss).toContain("height: 100%;");
    expect(shellCss).toContain("scrollbar-width: none;");
    expect(shellCss).toContain(".messageViewport::-webkit-scrollbar");
    expect(historyCss).toContain("scrollbar-width: none;");
    expect(historyCss).toContain(".list::-webkit-scrollbar");
    expect(navCss).toContain("padding: var(--terminal-space-3) 0;");
  });
});
