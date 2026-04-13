import type { HTMLAttributes, ReactNode } from "react";

import styles from "./terminal-frame.module.css";

type TerminalFrameProps = HTMLAttributes<HTMLElement> & {
  actions?: ReactNode;
  as?: "article" | "div" | "section";
  bodyClassName?: string;
  description?: ReactNode;
  footer?: ReactNode;
  label?: string;
  meta?: ReactNode;
  title?: ReactNode;
};

export function TerminalFrame({
  actions,
  as = "section",
  bodyClassName,
  children,
  className,
  description,
  footer,
  label,
  meta,
  title,
  ...rest
}: TerminalFrameProps) {
  const Component = as;

  return (
    <Component className={[styles.frame, className].filter(Boolean).join(" ")} {...rest}>
      {(label || title || description || meta || actions) && (
        <header className={styles.header}>
          <div className={styles.heading}>
            {label ? <p className={styles.label}>{label}</p> : null}
            {title ? <h2 className={styles.title}>{title}</h2> : null}
            {description ? <div className={styles.description}>{description}</div> : null}
          </div>

          {(meta || actions) && (
            <div className={styles.meta}>
              {meta ? <div className={styles.metaBlock}>{meta}</div> : null}
              {actions ? <div className={styles.actions}>{actions}</div> : null}
            </div>
          )}
        </header>
      )}

      <div className={[styles.body, bodyClassName].filter(Boolean).join(" ")}>{children}</div>

      {footer ? <footer className={styles.footer}>{footer}</footer> : null}
    </Component>
  );
}
