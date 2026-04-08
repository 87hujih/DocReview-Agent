import type { Metadata } from "next";
import type { ReactNode } from "react";

import Nav from "../components/nav";
import "./globals.css";

type RootLayoutProps = {
  children: ReactNode;
};

export const metadata: Metadata = {
  title: "DocReview Agent",
  description: "Task 4 terminal UI for document review workflows."
};

export default function RootLayout({ children }: RootLayoutProps) {
  return (
    <html lang="zh-CN">
      <body>
        <Nav />
        <main className="app-shell">{children}</main>
      </body>
    </html>
  );
}
