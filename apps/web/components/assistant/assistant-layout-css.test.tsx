import { readFileSync } from "node:fs";
import { join } from "node:path";

describe("assistant layout css", () => {
  it("locks the page viewport and hides internal scrollbars", () => {
    const globalCss = readFileSync(join(process.cwd(), "app", "globals.css"), "utf8");
    const navCss = readFileSync(join(process.cwd(), "components", "nav.module.css"), "utf8");
    const pageCss = readFileSync(join(process.cwd(), "app", "page.module.css"), "utf8");
    const shellCss = readFileSync(join(process.cwd(), "components", "assistant", "assistant-shell.module.css"), "utf8");
    const historyCss = readFileSync(join(process.cwd(), "components", "assistant", "session-history.module.css"), "utf8");
    const composerCss = readFileSync(join(process.cwd(), "components", "assistant", "assistant-composer.module.css"), "utf8");

    expect(globalCss).toContain("height: 100%;");
    expect(globalCss).toContain("height: 100dvh;");
    expect(globalCss).toContain("overflow: hidden;");
    expect(globalCss).toContain(".app-body");
    expect(globalCss).toContain(".app-main");
    expect(globalCss).toContain("display: flex;");
    expect(globalCss).toContain("height: 100%;");
    expect(globalCss).toContain("min-height: 0;");
    expect(pageCss).toContain("overflow: hidden;");
    expect(pageCss).toContain("height: 100%;");
    expect(shellCss).toContain("height: 100%;");
    expect(shellCss).toContain("display: flex;");
    expect(shellCss).toContain("scrollbar-width: none;");
    expect(shellCss).toContain(".messageViewport::-webkit-scrollbar");
    expect(historyCss).toContain("scrollbar-width: none;");
    expect(historyCss).toContain(".list::-webkit-scrollbar");
    expect(navCss).toContain("height: 100%;");
    expect(navCss).toContain("flex-direction: column;");
    expect(globalCss).toContain("[data-rail-collapsed=\"true\"] .app-rail");
    expect(navCss).toContain("overflow: hidden;");
    expect(navCss).toContain("flex: 1;");
    expect(historyCss).toContain("height: 100%;");
    expect(composerCss).toContain("width: min(100%, 860px);");
    expect(composerCss).toContain("margin: 0 auto;");
    expect(composerCss).toContain("backdrop-filter: blur(12px);");
    expect(composerCss).toContain("border-radius: 16px;");
    expect(composerCss).toContain("max-height: 250px;");
    expect(composerCss).toContain("box-shadow:");
    expect(composerCss).toContain(".sendBtn:not(:disabled)");
    expect(composerCss).toContain("background: #00C8A5;");
  });

  it("keeps the portal-mounted history rail on a fixed-height flex chain", () => {
    const globalCss = readFileSync(join(process.cwd(), "app", "globals.css"), "utf8");
    const navCss = readFileSync(join(process.cwd(), "components", "nav.module.css"), "utf8");

    expect(globalCss).toMatch(/\.app-rail\s*{[^}]*display:\s*flex;[^}]*flex-direction:\s*column;[^}]*min-height:\s*0;[^}]*overflow:\s*hidden;/s);
    expect(navCss).toMatch(/\.nav\s*{[^}]*display:\s*flex;[^}]*flex-direction:\s*column;[^}]*flex:\s*1;[^}]*height:\s*100%;[^}]*min-height:\s*0;[^}]*overflow:\s*hidden;/s);
    expect(navCss).toMatch(/\.body\s*{[^}]*display:\s*flex;[^}]*flex:\s*1;[^}]*min-height:\s*0;[^}]*flex-direction:\s*column;[^}]*overflow:\s*hidden;/s);
  });

  it("uses a wide assistant content flow without user avatar placeholders", () => {
    const messageCss = readFileSync(
      join(process.cwd(), "components", "assistant", "assistant-message-list.module.css"),
      "utf8"
    );

    expect(messageCss).not.toContain(".avatar {");
    expect(messageCss).not.toContain(".role {");
    expect(messageCss).not.toMatch(/\.message\[data-role="assistant"\]\s*{[^}]*background:/s);
    expect(messageCss).not.toMatch(/\.message\[data-role="assistant"\]\s*{[^}]*border:/s);
    expect(messageCss).toMatch(/\.message\[data-role="assistant"\]\s*{[^}]*max-width:\s*100%;/s);
    expect(messageCss).toMatch(/\.message\[data-role="user"\]\s*{[^}]*width:\s*min\(78%,\s*680px\);/s);
    expect(messageCss).toContain(".assistantFooter");
    expect(messageCss).toContain(".userCopyRail");
  });

  it("styles copy actions like subtle hover tools instead of outlined pills", () => {
    const messageCss = readFileSync(
      join(process.cwd(), "components", "assistant", "assistant-message-list.module.css"),
      "utf8"
    );

    expect(messageCss).toMatch(/\.copyAction\s*{[^}]*width:\s*28px;[^}]*height:\s*28px;/s);
    expect(messageCss).toMatch(/\.copyAction\s*{[^}]*border:\s*none;/s);
    expect(messageCss).toMatch(/\.copyAction\s*{[^}]*opacity:\s*0(?:\.0+)?;/s);
    expect(messageCss).toMatch(/\.messageRow:hover\s+\.copyAction\s*{[^}]*opacity:\s*1;/s);
    expect(messageCss).toMatch(/\.messageRow:focus-within\s+\.copyAction\s*{[^}]*opacity:\s*1;/s);
    expect(messageCss).toMatch(/\.copyAction:hover\s*{[^}]*background:\s*rgba\(148,\s*163,\s*184,\s*0\.14\);/s);
  });
});
