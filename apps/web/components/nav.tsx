"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import styles from "./nav.module.css";

const NAV_ITEMS = [
	{ href: "/resources", icon: "resources", label: "资源库" },
	{ href: "/", icon: "assistant", label: "助手" },
	{ href: "/runs", icon: "runs", label: "运行记录" },
	{ href: "/approvals", icon: "approvals", label: "审批" }
];

type NavProps = {
  extraSlotId?: string;
  variant?: "sidebar";
};

function isActivePath(pathname: string | null, href: string): boolean {
  if (!pathname) {
    return href === "/";
  }

  if (href === "/") {
    return pathname === "/";
  }

  return pathname.startsWith(href);
}

function renderIcon(icon: string) {
  switch (icon) {
    case "assistant":
      return (
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 3 4.5 7v5c0 4.1 2.7 7.8 7.5 9 4.8-1.2 7.5-4.9 7.5-9V7L12 3Z" />
          <path d="M9.5 12h5" />
          <path d="M12 9.5v5" />
        </svg>
      );
    case "resources":
      return (
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
          <path d="M4 6.5C4 5.1 5.1 4 6.5 4h11C18.9 4 20 5.1 20 6.5v11c0 1.4-1.1 2.5-2.5 2.5h-11A2.5 2.5 0 0 1 4 17.5v-11Z" />
          <path d="M8 8h8" />
          <path d="M8 12h8" />
          <path d="M8 16h5" />
        </svg>
      );
	case "runs":
	  return (
		<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
		  <path d="M5 19V5" /><path d="M5 17h5V7h5v6h4" /><path d="m16 10 3 3-3 3" />
		</svg>
	  );
	case "approvals":
	  return (
		<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
		  <path d="m5 12 4 4L19 6" /><path d="M4 4h16v16H4z" />
		</svg>
	  );
    default:
      return (
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 4 4 8.5v7L12 20l8-4.5v-7L12 4Z" />
          <path d="M9 11.5 11 13.5 15 9.5" />
        </svg>
      );
  }
}

export default function Nav({ extraSlotId, variant = "sidebar" }: NavProps) {
  const pathname = usePathname();
  const renderNavLink = (item: { href: string; icon: string; label: string }) => {
    const active = isActivePath(pathname, item.href);

    return (
      <Link key={item.href} className={styles.link} data-active={active} href={item.href}>
        <span aria-hidden="true" className={styles.icon}>
          {renderIcon(item.icon)}
        </span>
        <span className={styles.linkText}>{item.label}</span>
      </Link>
    );
  };

  return (
    <nav aria-label="主导航" className={styles.nav} data-variant={variant}>
      <div className={styles.identity}>
        <span className={styles.eyebrow}>AI Workspace</span>
        <span className={styles.product}>个人助手</span>
		<span className={styles.caption}>所有 Agent 请求统一进入持久化新运行时。</span>
      </div>

      <div className={styles.body}>
        <div className={styles.links} data-nav-group="primary">
          {NAV_ITEMS.map((item) => renderNavLink(item))}
        </div>

        {extraSlotId ? <div className={styles.extraSlot} id={extraSlotId} /> : null}
      </div>

      <div className={styles.footer}>
        <span className={styles.footerLabel}>沉浸式工作台</span>
        <span className={styles.footerText}>整体外层布局保持不变，切换页面时只更新主内容区。</span>
      </div>
    </nav>
  );
}
