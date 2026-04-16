"use client";

import { useParams, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";

import { ResourceVersionViewer } from "../../../components/resource-version-viewer";
import { TerminalFrame } from "../../../components/ui/terminal-frame";
import { getResource, type ResourceDetailsResponse } from "../../../lib/api/resources";
import { getErrorMessage } from "../../../lib/terminal";
import styles from "./page.module.css";

export default function ResourceDetailPage() {
  const params = useParams<{ id: string }>();
  const searchParams = useSearchParams();
  const resourceId = typeof params.id === "string" ? params.id : "";
  const sessionId = normalizeSessionId(searchParams.get("session"));

  const [details, setDetails] = useState<ResourceDetailsResponse | null>(null);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    if (!resourceId) {
      setIsLoading(false);
      return;
    }

    let active = true;

    async function load() {
      try {
        const response = await getResource(resourceId);
        if (!active) return;
        setDetails(response);
        setErrorMessage(null);
      } catch (error) {
        if (active) setErrorMessage(getErrorMessage(error));
      } finally {
        if (active) setIsLoading(false);
      }
    }

    void load();

    return () => {
      active = false;
    };
  }, [resourceId]);

  return (
    <div className={styles.page}>
      {errorMessage ? (
        <TerminalFrame label="资源详情" title="加载失败">
          <p className={styles.error}>错误 &gt; {errorMessage}</p>
        </TerminalFrame>
      ) : null}

      {isLoading && !details ? (
        <TerminalFrame label="资源详情" title="正在加载">
          <p className={styles.placeholder}>正在加载资源当前版本</p>
        </TerminalFrame>
      ) : null}

      {details ? (
        <ResourceVersionViewer
          resource={details.resource}
          sessionId={sessionId}
          version={details.current_version}
        />
      ) : null}
    </div>
  );
}

function normalizeSessionId(value: string | null): string | null {
  const normalized = (value || "").trim();
  return normalized || null;
}
