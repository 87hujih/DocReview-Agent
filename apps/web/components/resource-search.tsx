"use client";

import { FormEvent, useState } from "react";

import { searchResource, type Citation } from "../lib/api/resources";
import { getErrorMessage, toIsoSeconds, truncateId } from "../lib/terminal";
import { TerminalFrame } from "./ui/terminal-frame";
import styles from "./resource-search.module.css";

type ResourceSearchProps = {
  resourceId: string;
};

type SearchStatus = "idle" | "loading" | "success-empty" | "success-hit" | "error";

export function ResourceSearch({ resourceId }: ResourceSearchProps) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<Citation[]>([]);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [lastRunAt, setLastRunAt] = useState<string | null>(null);
  const [status, setStatus] = useState<SearchStatus>("idle");

  const isLoading = status === "loading";

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const trimmedQuery = query.trim();
    if (!trimmedQuery) {
      return;
    }

    setStatus("loading");
    setErrorMessage(null);

    try {
      const citations = await searchResource(resourceId, trimmedQuery);
      setResults(citations);
      setLastRunAt(new Date().toISOString());
      setStatus(citations.length > 0 ? "success-hit" : "success-empty");
    } catch (error) {
      setErrorMessage(getErrorMessage(error));
      setResults([]);
      setStatus("error");
    }
  }

  function renderBody() {
    if (status === "success-hit") {
      return (
        <ul className={styles.results}>
          {results.map((citation) => (
            <li key={citation.citation_id} className={styles.result}>
              <div className={styles.resultHeader}>
                <span>{citation.section_title}</span>
                <span>{truncateId(citation.resource_id, 8, 4)}</span>
              </div>
              <p className={styles.snippet}>{citation.snippet}</p>
              <span className={styles.citationId}>{citation.citation_id}</span>
            </li>
          ))}
        </ul>
      );
    }

    if (status === "error") {
      return null;
    }

    const placeholder =
      status === "loading"
        ? "正在检索引用"
        : status === "success-empty"
          ? "没有匹配到引用片段"
          : "请输入检索词";

    return <p className={styles.placeholder}>{placeholder}</p>;
  }

  return (
    <TerminalFrame
      label="资源检索"
      title="引用查找"
    >
      <form className={styles.form} onSubmit={handleSubmit}>
        <label className={styles.field} htmlFor="resource-search-query">
          <span className={styles.label}>检索词</span>
          <input
            id="resource-search-query"
            className={styles.input}
            name="query"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="输入关键词，例如：审批、考勤、风险提示"
            value={query}
          />
        </label>

        <button className={styles.button} disabled={isLoading} type="submit">
          {isLoading ? "检索中" : "检索引用"}
        </button>
      </form>

      {lastRunAt ? <p className={styles.timestamp}>上次检索时间 {toIsoSeconds(lastRunAt)}</p> : null}

      {errorMessage ? <p className={styles.error}>错误 &gt; {errorMessage}</p> : null}

      {renderBody()}
    </TerminalFrame>
  );
}
