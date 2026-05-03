"use client";

import type { ReactNode } from "react";

import styles from "./assistant-message-list.module.css";

type AssistantMarkdownProps = {
  content: string;
};

type MarkdownBlock =
  | {
      level: 1 | 2 | 3 | 4 | 5 | 6;
      text: string;
      type: "heading";
    }
  | {
      code: string;
      language: string;
      type: "code";
    }
  | {
      items: string[];
      ordered: boolean;
      type: "list";
    }
  | {
      text: string;
      type: "blockquote";
    }
  | {
      text: string;
      type: "paragraph";
    };

function parseBlocks(content: string): MarkdownBlock[] {
  const lines = content.replace(/\r\n?/g, "\n").split("\n");
  const blocks: MarkdownBlock[] = [];
  let index = 0;

  while (index < lines.length) {
    const line = lines[index];
    const trimmed = line.trim();

    if (trimmed === "") {
      index += 1;
      continue;
    }

    if (trimmed.startsWith("```")) {
      const language = trimmed.slice(3).trim();
      const codeLines: string[] = [];
      index += 1;

      while (index < lines.length && !lines[index].trim().startsWith("```")) {
        codeLines.push(lines[index]);
        index += 1;
      }

      if (index < lines.length) {
        index += 1;
      }

      blocks.push({
        code: codeLines.join("\n"),
        language,
        type: "code"
      });
      continue;
    }

    const headingMatch = trimmed.match(/^(#{1,6})\s+(.+)$/);
    if (headingMatch) {
      blocks.push({
        level: headingMatch[1].length as 1 | 2 | 3 | 4 | 5 | 6,
        text: headingMatch[2],
        type: "heading"
      });
      index += 1;
      continue;
    }

    if (/^>\s?/.test(trimmed)) {
      const quoteLines: string[] = [];

      while (index < lines.length && /^>\s?/.test(lines[index].trim())) {
        quoteLines.push(lines[index].trim().replace(/^>\s?/, ""));
        index += 1;
      }

      blocks.push({
        text: quoteLines.join("\n"),
        type: "blockquote"
      });
      continue;
    }

    const isOrderedList = /^\d+\.\s+/.test(trimmed);
    const isUnorderedList = /^[-*+]\s+/.test(trimmed);
    if (isOrderedList || isUnorderedList) {
      const items: string[] = [];
      const itemPattern = isOrderedList ? /^\d+\.\s+(.+)$/ : /^[-*+]\s+(.+)$/;

      while (index < lines.length) {
        const candidate = lines[index].trim();
        const itemMatch = candidate.match(itemPattern);
        if (!itemMatch) {
          break;
        }

        items.push(itemMatch[1]);
        index += 1;
      }

      blocks.push({
        items,
        ordered: isOrderedList,
        type: "list"
      });
      continue;
    }

    const paragraphLines: string[] = [];
    while (index < lines.length) {
      const candidate = lines[index];
      const candidateTrimmed = candidate.trim();

      if (
        candidateTrimmed === "" ||
        candidateTrimmed.startsWith("```") ||
        /^(#{1,6})\s+/.test(candidateTrimmed) ||
        /^>\s?/.test(candidateTrimmed) ||
        /^\d+\.\s+/.test(candidateTrimmed) ||
        /^[-*+]\s+/.test(candidateTrimmed)
      ) {
        break;
      }

      paragraphLines.push(candidateTrimmed);
      index += 1;
    }

    blocks.push({
      text: paragraphLines.join(" "),
      type: "paragraph"
    });
  }

  return blocks;
}

function isSafeHref(href: string): boolean {
  if (!href) {
    return false;
  }

  if (/^(\/|#|\?)/.test(href)) {
    return true;
  }

  try {
    const parsed = new URL(href);
    return parsed.protocol === "http:" || parsed.protocol === "https:" || parsed.protocol === "mailto:";
  } catch {
    return false;
  }
}

function renderInline(text: string): ReactNode[] {
  const tokenPattern = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\((?:[^()]|\([^)]*\))*\))/g;
  const nodes: ReactNode[] = [];
  let cursor = 0;

  for (const match of text.matchAll(tokenPattern)) {
    const token = match[0];
    const start = match.index ?? 0;

    if (start > cursor) {
      nodes.push(text.slice(cursor, start));
    }

    if (token.startsWith("`") && token.endsWith("`")) {
      nodes.push(
        <code key={`${start}-code`} className={styles.inlineCode}>
          {token.slice(1, -1)}
        </code>
      );
    } else if (token.startsWith("**") && token.endsWith("**")) {
      nodes.push(<strong key={`${start}-strong`}>{token.slice(2, -2)}</strong>);
    } else if (token.startsWith("*") && token.endsWith("*")) {
      nodes.push(<em key={`${start}-em`}>{token.slice(1, -1)}</em>);
    } else {
      const linkMatch = token.match(/^\[([^\]]+)\]\((.+)\)$/);
      if (linkMatch) {
        const [, label, href] = linkMatch;
        if (isSafeHref(href)) {
          nodes.push(
            <a key={`${start}-link`} href={href} rel="noreferrer" target="_blank">
              {label}
            </a>
          );
        } else {
          nodes.push(label);
        }
      } else {
        nodes.push(token);
      }
    }

    cursor = start + token.length;
  }

  if (cursor < text.length) {
    nodes.push(text.slice(cursor));
  }

  return nodes;
}

export function AssistantMarkdown({ content }: AssistantMarkdownProps) {
  const blocks = parseBlocks(content);

  return (
    <div className={styles.markdown}>
      {blocks.map((block, index) => {
        if (block.type === "heading") {
          const HeadingTag = `h${block.level}` as const;
          return <HeadingTag key={`heading-${index}`}>{renderInline(block.text)}</HeadingTag>;
        }

        if (block.type === "code") {
          return (
            <pre key={`code-${index}`} className={styles.codeBlock}>
              <code data-language={block.language || undefined}>{block.code}</code>
            </pre>
          );
        }

        if (block.type === "blockquote") {
          return (
            <blockquote key={`quote-${index}`} className={styles.blockquote}>
              <p>{renderInline(block.text)}</p>
            </blockquote>
          );
        }

        if (block.type === "list") {
          const ListTag = block.ordered ? "ol" : "ul";
          return (
            <ListTag key={`list-${index}`} className={styles.listBlock}>
              {block.items.map((item, itemIndex) => (
                <li key={`item-${index}-${itemIndex}`}>{renderInline(item)}</li>
              ))}
            </ListTag>
          );
        }

        return <p key={`paragraph-${index}`}>{renderInline(block.text)}</p>;
      })}
    </div>
  );
}
