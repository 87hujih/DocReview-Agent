import Link from "next/link";

// HomePage 渲染当前阶段的首页占位内容和已规划入口。
export default function HomePage() {
  return (
    <main
      style={{
        maxWidth: 960,
        margin: "0 auto",
        padding: "64px 24px",
        display: "grid",
        gap: 24
      }}
    >
      <section
        style={{
          padding: 32,
          backgroundColor: "#ffffff",
          borderRadius: 20,
          boxShadow: "0 12px 40px rgba(15, 23, 42, 0.08)"
        }}
      >
        <p style={{ marginTop: 0, color: "#52606d", fontWeight: 600 }}>Phase 1 Scaffold</p>
        <h1 style={{ margin: "0 0 16px", fontSize: 40 }}>Enterprise document intelligence MVP</h1>
        <p style={{ margin: 0, fontSize: 18, lineHeight: 1.6 }}>
          This app currently exposes the minimum Next.js surface needed for the RAG demo. The
          backend health endpoint is expected at <code>http://localhost:8080/healthz</code>.
        </p>
      </section>

      <section
        style={{
          padding: 32,
          backgroundColor: "#ffffff",
          borderRadius: 20,
          boxShadow: "0 12px 40px rgba(15, 23, 42, 0.08)"
        }}
      >
        <h2 style={{ marginTop: 0 }}>Planned entry points</h2>
        <ul style={{ margin: 0, paddingLeft: 20, lineHeight: 1.8 }}>
          <li>
            Resource browser route: <Link href="/resources">/resources</Link>
          </li>
          <li>Backend health route: /healthz</li>
          <li>PostgreSQL + pgvector via docker compose</li>
        </ul>
      </section>
    </main>
  );
}
