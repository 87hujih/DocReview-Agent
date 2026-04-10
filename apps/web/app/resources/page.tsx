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
        label="资源索引"
        title="资源库"
      >
        <p className={styles.banner}>输出 &gt; 正在调用 /api/resources</p>
      </TerminalFrame>

      <TerminalFrame
        label="资源列表"
        title={`资源数量 ${resources.length}`}
      >
        {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}

        {isLoading ? (
          <p className={styles.placeholder}>正在加载资源索引</p>
        ) : (
          <ResourceList resources={resources} />
        )}
      </TerminalFrame>
    </div>
  );
}
