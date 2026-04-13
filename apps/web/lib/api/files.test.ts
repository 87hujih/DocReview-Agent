import { getFileDownloadURL } from "./files";

describe("files api helpers", () => {
  it("builds the original file download url", () => {
    expect(getFileDownloadURL("file-123")).toBe("/api/files/file-123/download");
  });
});
