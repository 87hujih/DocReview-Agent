import TaskCreatePageClient from "./task-create-client";

type TaskCreatePageProps = {
  searchParams?: {
    resource_id?: string | string[];
  };
};

export default function TaskCreatePage({ searchParams }: TaskCreatePageProps) {
  const resourceId = Array.isArray(searchParams?.resource_id)
    ? searchParams?.resource_id[0] || ""
    : searchParams?.resource_id || "";

  return <TaskCreatePageClient resourceId={resourceId} />;
}
