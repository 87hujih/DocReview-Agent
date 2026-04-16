import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { ResourceSearch } from "./resource-search";
import { searchResource } from "../lib/api/resources";

vi.mock("../lib/api/resources", () => ({
  searchResource: vi.fn()
}));

const mockedSearchResource = vi.mocked(searchResource);

describe("ResourceSearch", () => {
  beforeEach(() => {
    mockedSearchResource.mockReset();
  });

  it("shows idle placeholder before any search", () => {
    render(<ResourceSearch resourceId="resource-1" />);

    const input = screen.getByLabelText("检索词");

    expect(screen.getByText("请输入检索词")).toBeInTheDocument();
    expect(screen.queryByText("没有匹配到引用片段")).not.toBeInTheDocument();

    fireEvent.change(input, { target: { value: "审批" } });

    expect(screen.getByText("请输入检索词")).toBeInTheDocument();
    expect(screen.queryByText("没有匹配到引用片段")).not.toBeInTheDocument();
  });

  it("shows empty-result placeholder only after successful zero-hit search", async () => {
    mockedSearchResource.mockResolvedValue([]);

    render(<ResourceSearch resourceId="resource-1" />);

    const input = screen.getByLabelText("检索词");
    const form = input.closest("form");
    if (!form) {
      throw new Error("expected search form");
    }

    fireEvent.change(input, { target: { value: "审批" } });
    fireEvent.submit(form);

    await waitFor(() => {
      expect(mockedSearchResource).toHaveBeenCalledWith("resource-1", "审批");
    });
    expect(await screen.findByText("没有匹配到引用片段")).toBeInTheDocument();
    expect(screen.queryByText(/错误 >/)).not.toBeInTheDocument();
  });

  it("shows error message without empty-result placeholder when search fails", async () => {
    mockedSearchResource.mockRejectedValue(new Error("检索失败"));

    render(<ResourceSearch resourceId="resource-1" />);

    const input = screen.getByLabelText("检索词");
    const form = input.closest("form");
    if (!form) {
      throw new Error("expected search form");
    }

    fireEvent.change(input, { target: { value: "审批" } });
    fireEvent.submit(form);

    expect(await screen.findByText("错误 > 检索失败")).toBeInTheDocument();
    expect(screen.queryByText("没有匹配到引用片段")).not.toBeInTheDocument();
  });

  it("updates timestamp only after successful search", async () => {
    mockedSearchResource
      .mockRejectedValueOnce(new Error("首次检索失败"))
      .mockResolvedValueOnce([
        {
          citation_id: "citation-1",
          resource_id: "resource-1",
          section_title: "审批流程",
          snippet: "审批流程摘要"
        }
      ]);

    render(<ResourceSearch resourceId="resource-1" />);

    const input = screen.getByLabelText("检索词");
    const form = input.closest("form");
    if (!form) {
      throw new Error("expected search form");
    }

    fireEvent.change(input, { target: { value: "审批" } });
    fireEvent.submit(form);

    expect(await screen.findByText("错误 > 首次检索失败")).toBeInTheDocument();
    expect(screen.queryByText(/上次检索时间/)).not.toBeInTheDocument();

    fireEvent.change(input, { target: { value: "考勤" } });
    fireEvent.submit(form);

    expect(await screen.findByText("审批流程")).toBeInTheDocument();
    expect(screen.getByText(/上次检索时间 \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z/)).toBeInTheDocument();
  });
});
