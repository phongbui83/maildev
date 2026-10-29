# MailDev frontend

Next.js 16 / React 19 / Tailwind 4 UI (Vietnamese, English, Japanese).

```bash
npm install
npm run dev          # proxies /api to API_UPSTREAM (default http://localhost:8080)
npm run typecheck && npm run build
```

NOTE: `API_UPSTREAM` is baked in at build time (Next.js rewrites). In Kubernetes the gateway routes `/api` directly to the backend, so it only matters for docker-compose.
