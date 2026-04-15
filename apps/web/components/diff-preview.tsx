import type { DiffSection } from "../lib/api/tasks";
import { DiffPane } from "./ui/diff-pane";
import { TerminalFrame } from "./ui/terminal-frame";
import styles from "./diff-preview.module.css";

type DiffPreviewProps = {
  sections: DiffSection[];
};

type ResolvedDiffSection = DiffSection & {
  displayTitle: string;
  resolvedOccurrence: number;
};

export function DiffPreview({ sections }: DiffPreviewProps) {
  const resolvedSections = resolveDiffSections(sections);

  return (
    <TerminalFrame
      label="修订预览"
      title="修订章节"
    >
      {resolvedSections.length === 0 ? (
        <p className={styles.empty}>当前没有修订章节</p>
      ) : (
        <div className={styles.stack}>
          {resolvedSections.map((section) => (
            <DiffPane
              key={`${section.section_title}-${section.resolvedOccurrence}`}
              citationIds={section.citation_ids}
              original={section.original}
              reason={section.reason}
              revised={section.revised}
              sectionTitle={section.displayTitle}
              testId="diff-section"
            />
          ))}
        </div>
      )}
    </TerminalFrame>
  );
}

function resolveDiffSections(sections: DiffSection[]): ResolvedDiffSection[] {
  const titleTotals = new Map<string, number>();
  const titleSeen = new Map<string, number>();

  sections.forEach((section) => {
    titleTotals.set(section.section_title, (titleTotals.get(section.section_title) ?? 0) + 1);
  });

  return sections.map((section) => {
    const seenCount = (titleSeen.get(section.section_title) ?? 0) + 1;
    titleSeen.set(section.section_title, seenCount);

    const resolvedOccurrence = section.section_occurrence ?? seenCount;
    const showOccurrence = (titleTotals.get(section.section_title) ?? 0) > 1;

    return {
      ...section,
      displayTitle: showOccurrence
        ? `${section.section_title} · 第 ${resolvedOccurrence} 处`
        : section.section_title,
      resolvedOccurrence
    };
  });
}
