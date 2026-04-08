"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import styles from "./nav.module.css";

const NAV_ITEMS = [
  { href: "/", label: "WORKBENCH" },
  { href: "/resources", label: "RESOURCES" },
  { href: "/approvals", label: "APPROVALS" }
];

function isActivePath(pathname: string, href: string): boolean {
  if (href === "/") {
    return pathname === "/";
  }

  return pathname.startsWith(href);
}

export default function Nav() {
  const pathname = usePathname();
  const apiLabel = (process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080").replace(
    /^https?:\/\//,
    ""
  );

  return (
    <nav className={styles.bar}>
      <div className={styles.identity}>
        <span className={styles.product}>DOCREVIEW_AGENT</span>
        <span className={styles.environment}>ENV: TASK_4 / API: {apiLabel}</span>
      </div>

      <div className={styles.links}>
        {NAV_ITEMS.map((item) => {
          const active = isActivePath(pathname, item.href);

          return (
            <Link key={item.href} className={styles.link} data-active={active} href={item.href}>
              <span className={styles.prefix}>{active ? ">" : " "}</span>
              <span>{item.label}</span>
            </Link>
          );
        })}
      </div>
    </nav>
  );
}
