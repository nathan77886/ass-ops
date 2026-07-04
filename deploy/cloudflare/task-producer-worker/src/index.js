import { isAuthorized, jsonResponse, requestID } from "./lib.js";

export default {
  async fetch(request, env) {
    if (request.method === "GET" && new URL(request.url).pathname === "/healthz") {
      return jsonResponse({ ok: true, component: "assops-task-producer-worker" });
    }
    if (request.method !== "POST") {
      return jsonResponse({ error: "method_not_allowed" }, 405);
    }
    if (!isAuthorized(request, env.ASSOPS_TASK_PRODUCER_TOKEN)) {
      return jsonResponse({ error: "unauthorized" }, 401);
    }
    if (!env.TASK_QUEUE) {
      return jsonResponse({ error: "task_queue_binding_missing" }, 500);
    }

    let body;
    try {
      body = await request.json();
    } catch {
      return jsonResponse({ error: "invalid_json" }, 400);
    }
    if (!body || typeof body !== "object" || Array.isArray(body)) {
      return jsonResponse({ error: "invalid_task_event" }, 400);
    }
    if (!body.event_id) {
      body.event_id = requestID();
    }
    if (!body.event_type) {
      body.event_type = "TaskRequested";
    }

    await env.TASK_QUEUE.send(body, { contentType: "json" });
    return jsonResponse({ ok: true, event_id: body.event_id });
  },
};
