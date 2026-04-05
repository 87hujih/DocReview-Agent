import type { Metadata } from "next";
import type { ReactNode } from "react";

export const metadata: Metadata = {
  title: "DocReview Agent",
  description: "Phase 1 scaffold for the enterprise document intelligence MVP."
};

// RootLayoutProps 描述根布局组件接收的子节点。
type RootLayoutProps = {
  children: ReactNode;
};

// RootLayout 当前故意保持最小骨架，等后续再补共享导航和主题系统。
export default function RootLayout({ children }: RootLayoutProps) {
  return (
    <html lang="en">
      <body
        style={{
          margin: 0,
          fontFamily: "Segoe UI, sans-serif",
          backgroundColor: "#f5f7fb",
          color: "#16202a"
        }}
      >
        {children}
      </body>
    </html>
  );
}
