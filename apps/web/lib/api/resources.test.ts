import { getResourceExportURL } from "./resources";

describe("resources api", () => {
  const originalEnv = process.env;

  beforeEach(() => {
    process.env = { ...originalEnv };
  });

  afterEach(() => {
    process.env = originalEnv;
  });

  it("builds the current resource version export URL", () => {
    process.env.NEXT_PUBLIC_API_URL = "http://127.0.0.1:18080";
    expect(getResourceExportURL("resource 1")).toBe(
      "http://127.0.0.1:18080/api/resources/resource%201/export"
    );
  });
});
