import assert from "node:assert/strict";
import { test } from "node:test";

import { sitePath } from "../src/lib/site-path.mjs";

test("sitePath joins the base and a route with one trailing slash", () => {
  assert.equal(sitePath("install", "/sum/"), "/sum/install/");
  assert.equal(sitePath("remainder-cli", "/sum/"), "/sum/remainder-cli/");
});

test("sitePath returns the base for the root route", () => {
  assert.equal(sitePath("", "/sum/"), "/sum/");
  assert.equal(sitePath(undefined, "/sum/"), "/sum/");
  assert.equal(sitePath("/", "/sum/"), "/sum/");
});

test("sitePath tolerates slashes on both sides of the join", () => {
  assert.equal(sitePath("/docs/", "/sum"), "/sum/docs/");
  assert.equal(sitePath("nested/path", "/sum/"), "/sum/nested/path/");
});

test("sitePath falls back to the server root outside an Astro build", () => {
  assert.equal(sitePath("install"), "/install/");
  assert.equal(sitePath(""), "/");
});
