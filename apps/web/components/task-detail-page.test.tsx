import TaskDetailPage from "../app/tasks/[id]/page";
import { redirect } from "next/navigation";

vi.mock("next/navigation", () => ({ redirect: vi.fn() }));

it("redirects the removed legacy task detail page to the durable assistant", () => {
  TaskDetailPage();
  expect(redirect).toHaveBeenCalledWith("/");
});
