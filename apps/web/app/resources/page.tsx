"use client";

import { useEffect, useState } from "react";

import { ResourceList } from "../../components/resource-list";
import { TerminalFrame } from "../../components/ui/terminal-frame";
import { getResources, type Resource } from "../../lib/api/resources";
import { getErrorMessage } from "../../lib/terminal";
import styles from "./page.module.css";

export default function ResourcesPage() {
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [resources, setResources] = useState<Resource[]>([]);
  const showErrorOnly = !isLoading && resources.length === 0 && Boolean(errorMessage);
  const frameTitle = isLoading
    ? "正在加载资源索引"
    : showErrorOnly
      ? "加载失败"
      : `资源数量 ${resources.length}`;

  useEffect(() => {
    let active = true;

    async function loadResources() {
      try {
        const items = await getResources();
        if (!active) {
          return;
        }

        setResources(items);
        setErrorMessage(null);
      } catch (error) {
        if (active) {
          setErrorMessage(getErrorMessage(error));
        }
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    }

    void loadResources();

    return () => {
      active = false;
    };
  }, []);

  return (
    <div className={styles.page}>
      <TerminalFrame
        bodyClassName={styles.listBody}
        className={styles.listFrame}
        label="资源库"
        title={frameTitle}
      >
        {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}

        {isLoading ? (
          <p className={styles.placeholder}>正在加载资源索引</p>
        ) : showErrorOnly ? null : (
          <ResourceList resources={resources} />
        )}
      </TerminalFrame>
    </div>
  );
}
