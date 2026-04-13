import { render, screen, within } from "@testing-library/react";

import Nav from "./nav";

const mockedUsePathname = vi.fn();

vi.mock("next/navigation", () => ({
  usePathname: () => mockedUsePathname()
}));

describe("Nav", () => {
  beforeEach(() => {
    mockedUsePathname.mockReturnValue("/resources");
  });

  it("keeps assistant directly below approvals in the primary navigation group", () => {
    const { container } = render(<Nav variant="sidebar" />);
    const primaryGroup = container.querySelector('[data-nav-group="primary"]');

    expect(primaryGroup).not.toBeNull();
    const links = within(primaryGroup as HTMLElement).getAllByRole("link");

    expect(links.map((link) => link.textContent?.trim())).toEqual(["资源库", "审批中心", "助手"]);
  });
});
