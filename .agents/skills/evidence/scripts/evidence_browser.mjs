#!/usr/bin/env node
// Browser recipe driver for evidence_capture.py: drives a headless Chromium-family browser over the Chrome DevTools Protocol with
// Node's built-in WebSocket (Node 22+), dispatches real input events, and writes a screenshot plus screencast frames.
// It is launched by evidence_capture.py with one JSON job on stdin and prints one JSON result on stdout; nothing else is written
// to stdout. Every browser log line goes to the job's diagnostics file. Never drives a user's profile: a fresh temporary
// user-data-dir is created per job and removed afterwards.
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile, appendFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

const job = JSON.parse(await readFile("/dev/stdin", "utf8"));
const out = { ok: false, steps: [], frames: [], observations: {}, errors: [], console: [] };
const diag = job.diagnostics;
const log = async (line) => { await appendFile(diag, `${new Date().toISOString()} ${line}\n`); };

if (typeof WebSocket !== "function") {
  out.errors.push(`node ${process.version} has no global WebSocket; Node 22 or newer is required`);
  console.log(JSON.stringify(out));
  process.exitCode = 2;
}

const profile = await mkdtemp(path.join(tmpdir(), "evidence-profile-"));
const args = [
  "--headless=new", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check",
  "--disable-extensions", "--disable-sync", "--disable-background-networking", "--mute-audio", "--hide-scrollbars",
  `--window-size=${job.viewport.width},${job.viewport.height}`, `--lang=${job.locale}`, "--force-device-scale-factor=1", "about:blank",
];
let browser;
let ws;
let exitCode = 1;
const pending = new Map();
let nextId = 1;
const listeners = [];

function send(method, params = {}, sessionId) {
  const id = nextId++;
  const message = JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) });
  ws.send(message);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP ${method} timed out`)); }, job.step_timeout_ms);
    pending.set(id, { resolve, reject, timer, method });
  });
}

function onMessage(event) {
  const message = JSON.parse(event.data);
  if (message.id && pending.has(message.id)) {
    const { resolve, reject, timer, method } = pending.get(message.id);
    clearTimeout(timer);
    pending.delete(message.id);
    if (message.error) reject(new Error(`CDP ${method}: ${message.error.message}`)); else resolve(message.result);
    return;
  }
  for (const listener of listeners) listener(message);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function waitFor(check, label) {
  const deadline = Date.now() + job.step_timeout_ms;
  while (Date.now() < deadline) {
    if (await check()) return true;
    await sleep(100);
  }
  throw new Error(`timed out waiting for ${label}`);
}

try {
  await log(`launch ${job.browser} ${args.join(" ")}`);
  browser = spawn(job.browser, args, { stdio: ["ignore", "pipe", "pipe"] });
  browser.stdout.on("data", (d) => log(`browser stdout: ${String(d).trimEnd()}`));
  browser.stderr.on("data", (d) => log(`browser stderr: ${String(d).trimEnd()}`));
  const spawnError = new Promise((_, reject) => browser.on("error", reject));
  const portFile = path.join(profile, "DevToolsActivePort");
  let endpoint;
  await Promise.race([spawnError, waitFor(async () => {
    try { endpoint = (await readFile(portFile, "utf8")).split("\n"); return endpoint.length >= 2 && endpoint[0].trim() !== ""; } catch { return false; }
  }, "the browser's DevToolsActivePort file")]);
  const port = endpoint[0].trim();
  const version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  out.observations.browser_version = version.Browser;
  out.observations.protocol_version = version["Protocol-Version"];
  ws = new WebSocket(version.webSocketDebuggerUrl);
  ws.addEventListener("message", onMessage);
  await new Promise((resolve, reject) => { ws.addEventListener("open", resolve, { once: true }); ws.addEventListener("error", () => reject(new Error("websocket failed")), { once: true }); });
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  const page = (method, params) => send(method, params, sessionId);
  await page("Page.enable");
  await page("Runtime.enable");
  await page("Emulation.setDeviceMetricsOverride", { width: job.viewport.width, height: job.viewport.height, deviceScaleFactor: 1, mobile: false });
  await page("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: job.theme }] });
  await page("Emulation.setLocaleOverride", { locale: job.locale }).catch(async (e) => log(`locale override unsupported: ${e.message}`));
  if (job.timezone) await page("Emulation.setTimezoneOverride", { timezoneId: job.timezone }).catch(async (e) => log(`timezone override unsupported: ${e.message}`));
  await sleep(job.settle_ms); // Let the metrics override take effect before the first screencast frame.
  listeners.push((m) => {
    if (m.sessionId !== sessionId) return;
    if (m.method === "Runtime.consoleAPICalled") out.console.push({ type: m.params.type, text: m.params.args.map((a) => a.value ?? a.description ?? "").join(" ").slice(0, 500) });
    if (m.method === "Runtime.exceptionThrown") out.console.push({ type: "exception", text: (m.params.exceptionDetails.exception?.description || m.params.exceptionDetails.text || "").slice(0, 500) });
  });

  // Screencast: frames arrive as JPEG; every frame is acknowledged and kept as an original with its wall-clock time.
  const frameDir = job.frame_dir;
  let frameIndex = 0;
  let frameBytes = 0;
  let recording = false;
  const recordStart = Date.now();
  listeners.push(async (m) => {
    if (m.sessionId !== sessionId || m.method !== "Page.screencastFrame" || !recording) return;
    const bytes = Buffer.from(m.params.data, "base64");
    await page("Page.screencastFrameAck", { sessionId: m.params.sessionId }).catch(() => {});
    const elapsed = Date.now() - recordStart;
    if (frameIndex >= job.max_frames || frameBytes + bytes.length > job.max_bytes || elapsed > job.max_ms) {
      if (recording) {
        recording = false;
        out.observations.screencast_limit = elapsed > job.max_ms ? "max_seconds" : (frameIndex >= job.max_frames ? "max_frames" : "max_bytes");
        await page("Page.stopScreencast").catch(() => {});
      }
      return;
    }
    const name = `frame-${String(frameIndex).padStart(5, "0")}.jpg`;
    frameIndex += 1;
    frameBytes += bytes.length;
    await writeFile(path.join(frameDir, name), bytes);
    out.frames.push({ file: name, ms: Date.now() - recordStart, width: m.params.metadata.deviceWidth, height: m.params.metadata.deviceHeight });
  });
  if (job.screencast) {
    recording = true;
    await page("Page.startScreencast", { format: "jpeg", quality: job.jpeg_quality, maxWidth: job.viewport.width, maxHeight: job.viewport.height, everyNthFrame: 1 });
  }

  const evaluate = async (expression) => {
    const result = await page("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result.value;
  };
  const center = async (selector) => {
    const box = await evaluate(`(() => { const el = document.querySelector(${JSON.stringify(selector)}); if (!el) return null; el.scrollIntoView({block: "center"}); const r = el.getBoundingClientRect(); return {x: r.x + r.width / 2, y: r.y + r.height / 2, w: r.width, h: r.height}; })()`);
    if (!box) throw new Error(`no element matches ${selector}`);
    if (box.w === 0 || box.h === 0) throw new Error(`element ${selector} has no size; it is not clickable`);
    return box;
  };
  const stepStart = Date.now();
  for (const step of job.steps) {
    const record = { ...step, started_ms: Date.now() - stepStart, ok: false };
    try {
      if (step.action === "goto") {
        const nav = await page("Page.navigate", { url: step.url });
        if (nav.errorText) throw new Error(`navigation failed: ${nav.errorText}`);
        await waitFor(async () => (await evaluate("document.readyState")) === "complete", "document.readyState complete");
        await sleep(job.settle_ms);
      } else if (step.action === "click") {
        const box = await center(step.selector);
        for (const type of ["mouseMoved", "mousePressed", "mouseReleased"]) {
          await page("Input.dispatchMouseEvent", { type, x: box.x, y: box.y, button: "left", clickCount: 1 });
        }
        await sleep(job.settle_ms);
      } else if (step.action === "type") {
        const box = await center(step.selector);
        for (const type of ["mouseMoved", "mousePressed", "mouseReleased"]) await page("Input.dispatchMouseEvent", { type, x: box.x, y: box.y, button: "left", clickCount: 1 });
        await page("Input.insertText", { text: step.text });
        await sleep(job.settle_ms);
      } else if (step.action === "press") {
        const keys = { Enter: { key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, text: "\r" }, Tab: { key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 }, Escape: { key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 } };
        const key = keys[step.key];
        if (!key) throw new Error(`unsupported key ${step.key}; supported: ${Object.keys(keys).join(", ")}`);
        await page("Input.dispatchKeyEvent", { type: "keyDown", ...key });
        await page("Input.dispatchKeyEvent", { type: "keyUp", ...key });
        await sleep(job.settle_ms);
      } else if (step.action === "wait-text") {
        await waitFor(async () => (await evaluate("document.body ? document.body.innerText : ''")).includes(step.text), `text ${JSON.stringify(step.text)}`);
      } else if (step.action === "wait") {
        await sleep(Math.min(step.ms, job.step_timeout_ms));
      } else {
        throw new Error(`unknown step action ${step.action}`);
      }
      record.ok = true;
    } catch (error) {
      record.error = error.message;
      out.steps.push({ ...record, ended_ms: Date.now() - stepStart });
      throw error;
    }
    out.steps.push({ ...record, ended_ms: Date.now() - stepStart });
  }
  await sleep(job.settle_ms);
  // Observations are read-only: the visible text and the accessibility-relevant title/URL, never a mutation.
  out.observations.url = await evaluate("location.href");
  out.observations.title = await evaluate("document.title");
  out.observations.text = String(await evaluate("document.body ? document.body.innerText : ''")).slice(0, job.text_limit);
  out.observations.viewport = await evaluate("({width: innerWidth, height: innerHeight, dpr: devicePixelRatio, dark: matchMedia('(prefers-color-scheme: dark)').matches, lang: navigator.language})");
  if (job.observe_selector) {
    out.observations.selector_text = await evaluate(`(() => { const el = document.querySelector(${JSON.stringify(job.observe_selector)}); return el ? el.innerText : null; })()`);
  }
  const shot = await page("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  await writeFile(job.screenshot, Buffer.from(shot.data, "base64"));
  if (recording) { recording = false; await page("Page.stopScreencast").catch(() => {}); }
  out.frame_count = frameIndex;
  out.ok = true;
  exitCode = 0;
} catch (error) {
  out.errors.push(error.message);
  await log(`error: ${error.stack || error.message}`);
} finally {
  try { ws?.close(); } catch {}
  if (browser && browser.exitCode === null) {
    browser.kill("SIGTERM");
    await Promise.race([new Promise((r) => browser.on("exit", r)), sleep(3000)]);
    if (browser.exitCode === null) browser.kill("SIGKILL");
  }
  await rm(profile, { recursive: true, force: true }).catch(() => {});
}
console.log(JSON.stringify(out));
process.exitCode = exitCode;
