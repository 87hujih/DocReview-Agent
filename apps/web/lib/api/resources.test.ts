import { getResourceExportURL } from "./resources";

describe("resources api", () => {
  it("builds the current resource version export URL", () => {
    expect(getResourceExportURL("resource 1")).toBe(
      "http://127.0.0.1:18080/api/resources/resource%201/export"
    );
  });
});
