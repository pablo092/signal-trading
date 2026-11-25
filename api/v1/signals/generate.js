// Vercel serverless proxy for the web demo: POST /api/v1/signals/generate.
//
// The browser cannot hold a service token safely (ADR-003), so this function holds
// it server-side (Vercel env var SIGNAL_SERVICE_TOKEN, a market:read-only token) and
// forwards a validated request to signal-service. It exposes nothing else.
const SIGNAL_SERVICE_URL =
  process.env.SIGNAL_SERVICE_URL || "https://trading-bot-signal-service.fly.dev";
const SYMBOL_RE = /^[A-Z]{1,10}$/;
const STRATEGY_RE = /^[a-z][a-z0-9_]{0,31}$/;
const TIMEFRAMES = new Set(["1Min", "5Min", "15Min", "30Min", "1Hour", "4Hour", "1Day"]);

module.exports = async function handler(req, res) {
  // Correlation ID (BL-20): forwarded to signal-service and returned to the browser.
  const requestId = `vercel-${crypto.randomUUID()}`;
  res.setHeader("X-Request-ID", requestId);
  res.setHeader("Cache-Control", "no-store");
  if (req.method !== "POST") {
    res.setHeader("Allow", "POST");
    return res.status(405).json({ error: "method_not_allowed" });
  }
  const token = process.env.SIGNAL_SERVICE_TOKEN;
  if (!token) return res.status(503).json({ error: "demo_not_configured" });

  let body = req.body;
  if (typeof body === "string") {
    try { body = JSON.parse(body); } catch { body = null; }
  }
  const symbol = String(body?.symbol ?? "").trim().toUpperCase();
  const strategy = String(body?.strategy ?? "rsi").trim().toLowerCase();
  const timeframe = body?.timeframe ?? "1Hour";
  if (!SYMBOL_RE.test(symbol) || !STRATEGY_RE.test(strategy) || !TIMEFRAMES.has(timeframe)) {
    return res.status(422).json({ detail: "invalid symbol, strategy or timeframe" });
  }

  try {
    const upstream = await fetch(`${SIGNAL_SERVICE_URL}/api/v1/signals/generate`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
        "X-Request-ID": requestId,
      },
      body: JSON.stringify({ symbol, strategy, timeframe }),
      redirect: "error",
      signal: AbortSignal.timeout(20000),
    });
    const text = await upstream.text();
    if (upstream.ok) {
      res.setHeader("Content-Type", "application/json");
      return res.status(200).send(text);
    }
    // Pass through only safe client errors; hide auth/upstream details.
    if (upstream.status === 422 || upstream.status === 404) {
      res.setHeader("Content-Type", "application/json");
      return res.status(upstream.status).send(text.slice(0, 1000));
    }
    return res.status(502).json({ error: "upstream_error", detail: `signal-service returned HTTP ${upstream.status}`, request_id: requestId });
  } catch {
    return res.status(504).json({ error: "upstream_unreachable", detail: "signal-service did not answer", request_id: requestId });
  }
};
