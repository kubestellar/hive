import express from 'express';
import { createProxyMiddleware } from 'http-proxy-middleware';
import path from 'path';
import crypto from 'crypto';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const PROXY_PORT = parseInt(process.env.HIVE_PROXY_PORT || '3001', 10);
const GO_API_PORT = parseInt(process.env.HIVE_API_PORT || '3002', 10);
const GO_API_URL = process.env.HIVE_API_URL || `http://127.0.0.1:${GO_API_PORT}`;
const TTYD_PORT = parseInt(process.env.HIVE_TTYD_PORT || '7681', 10);
const TTYD_URL = `http://127.0.0.1:${TTYD_PORT}`;
const DASHBOARD_TOKEN = process.env.HIVE_DASHBOARD_TOKEN || '';
const STATIC_DIR = process.env.HIVE_STATIC_DIR || path.join(__dirname, 'public');

if (!DASHBOARD_TOKEN && process.env.NODE_ENV === 'production') {
  console.error('[SECURITY] HIVE_DASHBOARD_TOKEN is not set — all mutations are unauthenticated!');
  process.exit(1);
}

const app = express();

function requireAuth(req, res, next) {
  if (!DASHBOARD_TOKEN) return next();
  const authHeader = req.headers.authorization || '';
  const match = authHeader.match(/^Bearer\s+(.+)$/i);
  if (!match) return res.status(401).json({ error: 'Unauthorized' });
  const supplied = Buffer.from(match[1]);
  const expected = Buffer.from(DASHBOARD_TOKEN);
  if (supplied.length !== expected.length || !crypto.timingSafeEqual(supplied, expected)) {
    return res.status(401).json({ error: 'Unauthorized' });
  }
  next();
}

app.use((req, res, next) => {
  res.setHeader('Content-Security-Policy', [
    "default-src 'self'",
    "script-src 'self' 'unsafe-inline'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: https:",
    "font-src 'self' https:",
    "connect-src 'self' https: ws: wss:",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
  ].join('; '));
  res.setHeader('X-Content-Type-Options', 'nosniff');
  res.setHeader('X-Frame-Options', 'DENY');
  res.setHeader('Referrer-Policy', 'strict-origin-when-cross-origin');
  next();
});

app.use((req, res, next) => {
  if (['POST', 'PUT', 'PATCH', 'DELETE'].includes(req.method)) {
    return requireAuth(req, res, next);
  }
  next();
});

app.use('/api', createProxyMiddleware({
  target: GO_API_URL,
  changeOrigin: true,
  pathRewrite: (path) => `/api${path}`,
  on: {
    error(err, req, res) {
      console.error(`[proxy] ${req.method} ${req.url} → ${err.message}`);
      if (res.writeHead) {
        res.writeHead(502, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: 'Go API unavailable', detail: err.message }));
      }
    },
  },
}));

const ttydProxy = createProxyMiddleware({
  target: TTYD_URL,
  changeOrigin: true,
  pathRewrite: (p) => p.replace(/^\/terminal/, ''),
  on: {
    error(err, req, res) {
      console.error(`[ttyd-proxy] ${req.method} ${req.url} → ${err.message}`);
      if (res.writeHead) {
        res.writeHead(502, { 'Content-Type': 'text/plain' });
        res.end('Terminal unavailable');
      }
    },
  },
});
app.use('/terminal', ttydProxy);

app.use(express.static(STATIC_DIR));
app.get('/{*splat}', (req, res) => {
  res.sendFile(path.join(STATIC_DIR, 'index.html'));
});

const server = app.listen(PROXY_PORT, () => {
  console.log(`[hive-proxy] Dashboard proxy on :${PROXY_PORT} → Go API at ${GO_API_URL}`);
});

server.on('upgrade', (req, socket, head) => {
  if (req.url.startsWith('/terminal')) {
    if (DASHBOARD_TOKEN) {
      const params = new URL(req.url, `http://${req.headers.host}`).searchParams;
      const token = params.get('token') || '';
      const supplied = Buffer.from(token);
      const expected = Buffer.from(DASHBOARD_TOKEN);
      if (supplied.length !== expected.length || !crypto.timingSafeEqual(supplied, expected)) {
        socket.write('HTTP/1.1 401 Unauthorized\r\n\r\n');
        socket.destroy();
        return;
      }
    }
    ttydProxy.upgrade(req, socket, head);
  }
});
