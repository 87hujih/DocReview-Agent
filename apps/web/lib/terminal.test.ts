import { formatStatusLabel, isTerminalStatus, toIsoSeconds } from "./terminal";

describe("terminal utils", () => {
  it("localizes completed status labels", () => {
    expect(formatStatusLabel("completed")).toBe("已完成");
  });

  it("treats trimmed terminal statuses as terminal", () => {
    expect(isTerminalStatus(" completed ")).toBe(true);
    expect(isTerminalStatus("failed")).toBe(true);
  });

  it("keeps the backend timezone offset when formatting timestamps", () => {
    expect(toIsoSeconds("2026-04-10T19:59:07.138641+08:00")).toBe("2026-04-10T19:59:07+08:00");
  });
});
