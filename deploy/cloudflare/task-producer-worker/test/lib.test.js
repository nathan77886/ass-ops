import assert from "node:assert/strict";
import test from "node:test";

import { isAuthorized } from "../src/lib.js";

test("isAuthorized requires exact bearer token", () => {
  const request = new Request("https://producer.example.test", {
    headers: { authorization: "Bearer secret" },
  });
  assert.equal(isAuthorized(request, "secret"), true);
  assert.equal(isAuthorized(request, "other"), false);
  assert.equal(isAuthorized(request, ""), false);
});
