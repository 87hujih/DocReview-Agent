import TaskCreatePage from "../app/tasks/new/page";
import { redirect } from "next/navigation";

vi.mock("next/navigation", () => ({ redirect: vi.fn() }));

it("redirects the removed legacy task creation page to the durable assistant", () => {
  TaskCreatePage();
  expect(redirect).toHaveBeenCalledWith("/");
});
