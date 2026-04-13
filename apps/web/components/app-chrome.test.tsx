import { render, screen } from "@testing-library/react";

import { AppChrome } from "./app-chrome";

const mockedUsePathname = vi.fn();

vi.mock("next/navigation", () => ({
  usePathname: () => mockedUsePathname()
}));

describe("AppChrome", () => {
  it("keeps the global sidebar visible on the assistant page", () => {
    mockedUsePathname.mockReturnValue("/");

    render(
      <AppChrome>
        <div>助手主区</div>
      </AppChrome>
    );

    expect(screen.getByRole("navigation", { name: "主导航" })).toBeInTheDocument();
    expect(screen.getByText("助手主区")).toBeInTheDocument();
  });
});
