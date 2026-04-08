import type { DiffSection } from "../lib/api/tasks";
import { DiffPane } from "./ui/diff-pane";
import { TerminalFrame } from "./ui/terminal-frame";
import styles from "./diff-preview.module.css";

type DiffPreviewProps = {
  sections: DiffSection[];
};

export function DiffPreview({ sections }: DiffPreviewProps) {
  return (
    <TerminalFrame
      label="DIFF_PREVIEW"
      title="REVISION_SECTIONS"
      description="每个 section 都保留原文、修订稿、引用编号和原因说明。"
    >
      {sections.length === 0 ? (
        <p className={styles.empty}>NO_DIFF_SECTIONS</p>
      ) : (
        <div className={styles.stack}>
          {sections.map((section) => (
            <DiffPane
              key={`${section.section_title}-${section.citation_ids.join(",")}`}
              citationIds={section.citation_ids}
              original={section.original}
              reason={section.reason}
              revised={section.revised}
              sectionTitle={section.section_title}
              testId="diff-section"
            />
          ))}
        </div>
      )}
    </TerminalFrame>
  );
}
