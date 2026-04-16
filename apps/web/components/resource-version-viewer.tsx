import Link from "next/link";

import { getResourceExportURL, type Resource, type ResourceVersion } from "../lib/api/resources";
import styles from "./resource-version-viewer.module.css";
import { TerminalFrame } from "./ui/terminal-frame";

type ResourceVersionViewerProps = {
  resource: Resource;
  version: ResourceVersion | null;
};

export function ResourceVersionViewer({ resource, version }: ResourceVersionViewerProps) {
  if (!version) {
    return (
      <TerminalFrame label="资源详情" title="当前版本">
        <p className={styles.empty}>这个资源还没有可展示或导出的当前版本。</p>
      </TerminalFrame>
    );
  }

  return (
    <TerminalFrame label="资源详情" title="当前版本">
      <article className={styles.viewer}>
        <header className={styles.hero}>
          <p className={styles.eyebrow}>CURRENT VERSION</p>
          <h1 className={styles.title}>{resource.title}</h1>
          <p className={styles.meta}>
            版本 {version.version_number} / {version.source}
          </p>
          <div className={styles.actions}>
            <Link className={styles.link} href={getResourceExportURL(resource.id)}>
              下载修订结果
            </Link>
          </div>
        </header>

        <pre className={styles.content}>{version.content}</pre>
      </article>
    </TerminalFrame>
  );
}
