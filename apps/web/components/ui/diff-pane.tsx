import styles from "./diff-pane.module.css";

type DiffPaneProps = {
  citationIds: string[];
  original: string;
  reason: string;
  revised: string;
  sectionTitle: string;
  testId?: string;
};

export function DiffPane({
  citationIds,
  original,
  reason,
  revised,
  sectionTitle,
  testId
}: DiffPaneProps) {
  return (
    <article className={styles.pane} data-testid={testId}>
      <header className={styles.header}>
        <div className={styles.headerBlock}>
          <span className={styles.label}>章节标题</span>
          <span className={styles.value}>{sectionTitle}</span>
        </div>
        <div className={styles.headerBlock}>
          <span className={styles.label}>引用编号</span>
          <div className={styles.citations}>
            {citationIds.map((citationId) => (
              <span key={citationId} className={styles.citation}>
                {citationId}
              </span>
            ))}
          </div>
        </div>
      </header>

      <div className={styles.columns}>
        <section className={styles.column}>
          <span className={styles.label}>原文</span>
          <p className={styles.original}>{original}</p>
        </section>

        <section className={styles.column}>
          <span className={styles.label}>修订后</span>
          <p className={styles.revised}>{revised}</p>
        </section>
      </div>

      <footer className={styles.reason}>
        <span className={styles.label}>修改原因</span>
        <p className={styles.reasonText}>{reason}</p>
      </footer>
    </article>
  );
}
