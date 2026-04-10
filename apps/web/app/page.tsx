import { AssistantShell } from "../components/assistant/assistant-shell";
import styles from "./page.module.css";

export default function HomePage() {
  return (
    <div className={styles.page}>
      <AssistantShell />
    </div>
  );
}
