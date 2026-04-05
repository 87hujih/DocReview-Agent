// ResourcesPage 先占住未来的 app-router 路由段，避免后续功能开发时再搬路由。
export default function ResourcesPage() {
  return (
    <main
      style={{
        maxWidth: 960,
        margin: "0 auto",
        padding: "64px 24px"
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
        <p style={{ marginTop: 0, color: "#52606d", fontWeight: 600 }}>Reserved route</p>
        <h1 style={{ margin: "0 0 16px", fontSize: 36 }}>Resources</h1>
        <p style={{ margin: 0, fontSize: 18, lineHeight: 1.6 }}>
          The full resource browser, document detail view, and citation search UI will land in Task
          5. This placeholder fixes the App Router boundary now so later work does not need to move
          routes around.
        </p>
      </section>
    </main>
  );
}
