# ASSOPS task producer worker

Cloudflare Worker bridge for publishing ASSOPS remote task events into Cloudflare Queues.

Required setup:

```bash
wrangler queues create assops-worker-tasks
wrangler secret put ASSOPS_TASK_PRODUCER_TOKEN
npm install
npm run deploy
```

Use deployed Worker URL as:

```bash
ASSOPS_CLOUDFLARE_TASK_PRODUCER_URL=https://...
ASSOPS_CLOUDFLARE_TASK_PRODUCER_TOKEN=...
```
