import { AssistantShell } from "../components/assistant/assistant-shell";
import styles from "./page.module.css";

type HomePageProps = {
  searchParams?: {
    session?: string | string[];
  };
};

export default function HomePage({ searchParams }: HomePageProps) {
  const initialSessionId = Array.isArray(searchParams?.session)
    ? searchParams?.session[0] || null
    : searchParams?.session || null;

  return (
    <div className={styles.page}>
      <AssistantShell initialSessionId={initialSessionId} />
    </div>
  );
}
