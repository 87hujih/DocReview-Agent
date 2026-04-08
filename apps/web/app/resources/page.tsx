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
        label="RESOURCE_INDEX"
        title="资源库"
        description="资源列表直接映射 /api/resources，点击任意条目即可跳入任务创建页。"
      >
        <p className={styles.banner}>STDOUT &gt; GET /api/resources</p>
      </TerminalFrame>

      <TerminalFrame
        label="RESOURCE_STREAM"
        title={`RESOURCE_COUNT ${resources.length}`}
        description="资源元数据强调 RESOURCE_ID、SOURCE_TYPE、CREATED_AT，不做营销式卡片装饰。"
      >
        {errorMessage ? <p className={styles.error}>STDERR &gt; {errorMessage}</p> : null}

        {isLoading ? (
          <p className={styles.placeholder}>LOADING_RESOURCE_INDEX</p>
        ) : (
          <ResourceList resources={resources} />
        )}
      </TerminalFrame>
    </div>
  );
}
