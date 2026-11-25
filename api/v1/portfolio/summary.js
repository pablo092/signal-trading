// Vercel serverless proxy for the web demo: GET /api/v1/portfolio/summary.
//
// Read-only. Holds the demo token server-side (SIGNAL_SERVICE_TOKEN; the
// `vercel-demo` entry has market:read + portfolio:read, never a write scope) and
// combines bot-engine's equity and positions into one whitelisted response.
const BOT_ENGINE_URL = process.env.BOT_ENGINE_URL || "https://trading-bot-engine.fly.dev";

async function get(path, token) {
  const r = await fetch(`${BOT_ENGINE_URL}${path}`, {
    headers: { Authorization: `Bearer ${token}` },
    redirect: "error",
    signal: AbortSignal.timeout(15000),
  });
  if (!r.ok) throw Object.assign(new Error("upstream"), { status: r.status });
  return r.json();
}

const num = (v) => (typeof v === "number" && Number.isFinite(v) ? v : 0);

module.exports = async function handler(req, res) {
  res.setHeader("Cache-Control", "no-store");
  if (req.method !== "GET") {
    res.setHeader("Allow", "GET");
    return res.status(405).json({ error: "method_not_allowed" });
  }
  const token = process.env.SIGNAL_SERVICE_TOKEN;
  if (!token) return res.status(503).json({ error: "demo_not_configured" });

  try {
    const [equity, positions, mode] = await Promise.all([
      get("/api/v1/portfolio/equity", token),
      get("/api/v1/portfolio/positions", token),
      get("/api/v1/trading-mode", token),
    ]);
    const items = (Array.isArray(positions) ? positions : []).map((p) => ({
      symbol: String(p.symbol ?? ""),
      quantity: num(p.quantity),
      average_entry_price: num(p.average_entry_price),
      current_price: num(p.current_price),
      unrealized_pnl: num(p.unrealized_pnl),
    }));
    return res.status(200).json({
      equity: num(equity.equity),
      market_open: Boolean(equity.market_open),
      paper_mode: Boolean(equity.paper_mode),
      order_execution_enabled: Boolean(mode.order_execution_enabled),
      positions: items,
    });
  } catch (e) {
    if (e && e.status) return res.status(502).json({ error: "upstream_error", status: e.status });
    return res.status(504).json({ error: "upstream_unreachable" });
  }
};
