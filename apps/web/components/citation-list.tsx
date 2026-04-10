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
      label="引用"
      title="检索证据"
    >
      {citations.length === 0 ? (
        <p className={styles.empty}>当前没有检索到引用证据</p>
      ) : (
        <ol className={styles.list}>
          {citations.map((citation) => (
            <li key={citation.citation_id} className={styles.item}>
              <div className={styles.header}>
                <span className={styles.citationId}>{citation.citation_id}</span>
                <span className={styles.resourceId}>{truncateId(citation.resource_id, 8, 4)}</span>
              </div>

              <div className={styles.meta}>
                <MetaRow label="章节标题" value={citation.section_title} />
                <MetaRow label="引用摘录" value={citation.snippet} />
              </div>
            </li>
          ))}
        </ol>
      )}
    </TerminalFrame>
  );
}
