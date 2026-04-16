import { buildPublicApiUrl, getPublicApiBaseURL } from "./public-url";

describe("public api url helpers", () => {
  const originalEnv = process.env;

  beforeEach(() => {
    process.env = { ...originalEnv };
  });

  afterEach(() => {
    process.env = originalEnv;
  });

  it("builds a full backend url from NEXT_PUBLIC_API_URL", () => {
    process.env.NEXT_PUBLIC_API_URL = "http://127.0.0.1:18080";

    expect(buildPublicApiUrl("/api/resources/r1/export")).toBe(
      "http://127.0.0.1:18080/api/resources/r1/export"
    );
  });

  it("falls back to the default backend url when env is not set", () => {
    delete process.env.NEXT_PUBLIC_API_URL;

    expect(getPublicApiBaseURL()).toBe("http://127.0.0.1:18080");
  });

  it("keeps encoded characters intact when joining the path", () => {
    process.env.NEXT_PUBLIC_API_URL = "http://127.0.0.1:18080";

    expect(buildPublicApiUrl("/api/resources/resource%201/export")).toBe(
      "http://127.0.0.1:18080/api/resources/resource%201/export"
    );
  });
});
