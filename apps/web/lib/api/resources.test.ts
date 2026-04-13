import { getResourceExportURL } from "./resources";

describe("resources api", () => {
  it("builds the current resource version export URL", () => {
    expect(getResourceExportURL("resource 1")).toBe("/api/resources/resource%201/export");
  });
});
