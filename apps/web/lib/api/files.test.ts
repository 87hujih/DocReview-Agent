import { getFileDownloadURL } from "./files";

describe("files api helpers", () => {
  const originalEnv = process.env;

  beforeEach(() => {
    process.env = { ...originalEnv };
  });

  afterEach(() => {
    process.env = originalEnv;
  });

  it("builds the original file download url using NEXT_PUBLIC_API_URL", () => {
    process.env.NEXT_PUBLIC_API_URL = "http://127.0.0.1:18080";
    expect(getFileDownloadURL("file-123")).toBe(
      "http://127.0.0.1:18080/api/files/file-123/download"
    );
  });

  it("falls back to default base URL when env is not set", () => {
    delete process.env.NEXT_PUBLIC_API_URL;
    expect(getFileDownloadURL("file-abc")).toBe(
      "http://127.0.0.1:18080/api/files/file-abc/download"
    );
  });

  it("encodes special characters in fileId", () => {
    process.env.NEXT_PUBLIC_API_URL = "http://127.0.0.1:18080";
    expect(getFileDownloadURL("file/with spaces")).toBe(
      "http://127.0.0.1:18080/api/files/file%2Fwith%20spaces/download"
    );
  });

  it("uses cloud server address when configured", () => {
    process.env.NEXT_PUBLIC_API_URL = "http://1.2.3.4:8080";
    expect(getFileDownloadURL("abc")).toBe(
      "http://1.2.3.4:8080/api/files/abc/download"
    );
  });
});
