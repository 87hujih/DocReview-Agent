import { toIsoSeconds } from "./terminal";

describe("terminal utils", () => {
  it("keeps the backend timezone offset when formatting timestamps", () => {
    expect(toIsoSeconds("2026-04-10T19:59:07.138641+08:00")).toBe("2026-04-10T19:59:07+08:00");
  });
});
