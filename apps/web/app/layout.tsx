import type { Metadata } from "next";
import type { ReactNode } from "react";

export const metadata: Metadata = {
  title: "DocReview Agent",
  description: "Phase 1 scaffold for the enterprise document intelligence MVP."
};

type RootLayoutProps = {
  children: ReactNode;
};

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
