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
      label="修订预览"
      title="修订章节"
    >
      {sections.length === 0 ? (
        <p className={styles.empty}>当前没有修订章节</p>
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
