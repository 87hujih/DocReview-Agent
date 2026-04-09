import type { ReactNode } from "react";

import styles from "./log-line.module.css";

type LogLineProps = {
  children: ReactNode;
  prefix?: string;
  timestamp?: string | null;
  tone?: "default" | "error" | "info" | "success" | "warning";
};

export function LogLine({
  children,
  prefix = "[INFO]",
  timestamp,
  tone = "default"
}: LogLineProps) {
  return (
    <div className={styles.line} data-tone={tone}>
      <span className={styles.prefix}>{prefix}</span>
      {timestamp ? <time className={styles.timestamp}>{timestamp}</time> : null}
      <span className={styles.message}>{children}</span>
    </div>
  );
}
