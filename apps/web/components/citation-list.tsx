import type { Citation } from "../lib/api/resources";
import { truncateId } from "../lib/terminal";
import { MetaRow } from "./ui/meta-row";
import { TerminalFrame } from "./ui/terminal-frame";
import styles from "./citation-list.module.css";

type CitationListProps = {
  citations: Citation[];
};

export function CitationList({ citations }: CitationListProps) {
  return (
    <TerminalFrame
      label="CITATIONS"
      title="RETRIEVAL_EVIDENCE"
      description="引用结果直接映射后端 citations artifact，便于和 diff_preview 做交叉校验。"
    >
      {citations.length === 0 ? (
        <p className={styles.empty}>NO_CITATIONS_CAPTURED</p>
      ) : (
        <ol className={styles.list}>
          {citations.map((citation) => (
            <li key={citation.citation_id} className={styles.item}>
              <div className={styles.header}>
                <span className={styles.citationId}>{citation.citation_id}</span>
                <span className={styles.resourceId}>{truncateId(citation.resource_id, 8, 4)}</span>
              </div>

              <div className={styles.meta}>
                <MetaRow label="section_title" value={citation.section_title} />
                <MetaRow label="snippet" value={citation.snippet} />
              </div>
            </li>
          ))}
        </ol>
      )}
    </TerminalFrame>
  );
}
