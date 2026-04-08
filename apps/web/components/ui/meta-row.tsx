import type { ReactNode } from "react";

import { formatToken } from "../../lib/terminal";
import styles from "./meta-row.module.css";

type MetaRowProps = {
  highlight?: boolean;
  label: string;
  value: ReactNode;
};

export function MetaRow({ highlight = false, label, value }: MetaRowProps) {
  return (
    <div className={styles.row} data-highlight={highlight ? "true" : "false"}>
      <span className={styles.label}>{formatToken(label)}</span>
      <span className={styles.value}>{value}</span>
    </div>
  );
}
