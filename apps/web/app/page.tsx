import { AssistantShell } from "../components/assistant/assistant-shell";
import styles from "./page.module.css";

type HomePageProps = {
  searchParams?: {
    resource_id?: string | string[];
    session?: string | string[];
  };
};

export default function HomePage({ searchParams }: HomePageProps) {
  const initialSessionId = Array.isArray(searchParams?.session)
    ? searchParams?.session[0] || null
    : searchParams?.session || null;
  const initialResourceId = Array.isArray(searchParams?.resource_id)
    ? searchParams?.resource_id[0] || null
    : searchParams?.resource_id || null;

  return (
    <div className={styles.page}>
      <AssistantShell initialResourceId={initialResourceId} initialSessionId={initialSessionId} />
    </div>
  );
}
