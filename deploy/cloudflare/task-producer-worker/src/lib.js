export function isAuthorized(request, token) {
  token = String(token || "").trim();
  if (!token) {
    return false;
  }
  const header = request.headers.get("authorization") || "";
  return header === `Bearer ${token}`;
}

export function requestID() {
  return crypto.randomUUID();
}

export function jsonResponse(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
    },
  });
}
