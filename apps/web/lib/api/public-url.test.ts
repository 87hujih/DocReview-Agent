import { buildPublicApiUrl, getPublicApiBaseURL } from "./public-url";

describe("public url helpers", () => {
  const originalEnv = process.env;

  beforeEach(() => {
    process.env = { ...originalEnv };
  });

  afterEach(() => {
    process.env = originalEnv;
  });

  it("builds a public api url using NEXT_PUBLIC_API_URL", () => {
    process.env.NEXT_PUBLIC_API_URL = "http://127.0.0.1:18080";

    expect(buildPublicApiUrl("/api/resources/r1/export")).toBe(
      "http://127.0.0.1:18080/api/resources/r1/export"
    );
  });

  it("falls back to the default public api base url", () => {
    delete process.env.NEXT_PUBLIC_API_URL;

    expect(getPublicApiBaseURL()).toBe("http://127.0.0.1:18080");
  });

  it("does not re-encode an already encoded path", () => {
    process.env.NEXT_PUBLIC_API_URL = "http://127.0.0.1:18080";

    expect(buildPublicApiUrl("/api/files/file%201/download")).toBe(
      "http://127.0.0.1:18080/api/files/file%201/download"
    );
  });
});
