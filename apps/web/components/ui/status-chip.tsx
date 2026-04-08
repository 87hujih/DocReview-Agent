import { formatStatusLabel, getStatusTone } from "../../lib/terminal";
import styles from "./status-chip.module.css";

type StatusChipProps = {
  className?: string;
  label?: string;
  status: string;
};

export function StatusChip({ className, label, status }: StatusChipProps) {
  const tone = getStatusTone(status);
  const resolvedLabel = label || formatStatusLabel(status);
  const content = tone === "running" ? "[ RUNNING ... █ ]" : `[${resolvedLabel}]`;

  return (
    <span className={[styles.chip, className].filter(Boolean).join(" ")} data-status={tone}>
      {content}
    </span>
  );
}
