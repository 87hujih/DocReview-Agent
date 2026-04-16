import { readFileSync } from "node:fs";
import { join } from "node:path";

describe("resources layout css", () => {
  it("locks the page height and lets the resource list scroll inside the frame", () => {
    const pageCss = readFileSync(join(process.cwd(), "app", "resources", "page.module.css"), "utf8");

    expect(pageCss).toContain("grid-template-rows: minmax(0, 1fr);");
    expect(pageCss).toContain("height: 100%;");
    expect(pageCss).toContain("min-height: 0;");
    expect(pageCss).toContain("overflow: hidden;");
    expect(pageCss).toContain(".listFrame");
    expect(pageCss).toContain("grid-template-rows: auto minmax(0, 1fr);");
    expect(pageCss).toContain(".listBody");
    expect(pageCss).toContain("overflow-y: auto;");
    expect(pageCss).toContain("scrollbar-width: none;");
  });
});
