import { readFileSync } from "node:fs";
import { join } from "node:path";

describe("global layout css", () => {
  it("uses an immersive sidebar shell and centered reading flow", () => {
    const globalCss = readFileSync(join(process.cwd(), "app", "globals.css"), "utf8");
    const navCss = readFileSync(join(process.cwd(), "components", "nav.module.css"), "utf8");
    const shellCss = readFileSync(join(process.cwd(), "components", "assistant", "assistant-shell.module.css"), "utf8");
    const historyCss = readFileSync(join(process.cwd(), "components", "assistant", "session-history.module.css"), "utf8");
    const messageCss = readFileSync(join(process.cwd(), "components", "assistant", "assistant-message-list.module.css"), "utf8");
    const frameCss = readFileSync(join(process.cwd(), "components", "ui", "terminal-frame.module.css"), "utf8");

    expect(globalCss).toContain(".app-body");
    expect(globalCss).toContain("display: flex;");
    expect(globalCss).toContain(".app-main");
    expect(globalCss).toContain("--app-sidebar-bg:");

    expect(navCss).toContain("flex-direction: column;");
    expect(navCss).toContain("justify-content: space-between;");
    expect(navCss).toContain("border: none;");

    expect(shellCss).toContain("display: flex;");
    expect(shellCss).toContain("border: none;");
    expect(shellCss).not.toContain(".workspaceHeader");

    expect(historyCss).toContain("grid-template-rows: auto minmax(0, 1fr);");
    expect(historyCss).not.toContain("border-right:");

    expect(messageCss).toContain("width: min(100%, 800px);");
    expect(messageCss).toContain("margin: 0 auto;");

    expect(frameCss).toContain("border: none;");
    expect(frameCss).toContain("border-radius:");
  });
});
