/** Test the installed upstream MCP transport without touching a Herdr session. */
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import path from "node:path";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const child = spawn(process.execPath, [path.join(root, ".deps/herdr-mesh/dist/index.js")], { stdio: ["pipe", "pipe", "inherit"] });
const responses = new Map();
const lines = createInterface({ input: child.stdout });
let failure;
child.on("error", error => { failure = error; });
lines.on("line", line => {
  try { const value = JSON.parse(line); responses.set(value.id, value); }
  catch { failure = new Error("MCP wrote non-JSON to stdout"); }
});
const send = value => child.stdin.write(JSON.stringify(value) + "\n");
async function wait(id) {
  const deadline = Date.now() + 15000;
  while (!responses.has(id)) {
    if (failure) throw failure;
    if (child.exitCode !== null) throw new Error(`MCP exited ${child.exitCode}`);
    if (Date.now() > deadline) throw new Error(`MCP response ${id} timed out`);
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  const response = responses.get(id);
  if (response.error) throw new Error(JSON.stringify(response.error));
  return response.result;
}
try {
  send({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "sum-smoke", version: "0.1.0" } } });
  await wait(1);
  send({ jsonrpc: "2.0", method: "notifications/initialized" });
  send({ jsonrpc: "2.0", id: 2, method: "tools/list", params: {} });
  const { tools } = await wait(2);
  const names = tools.map(t => t.name);
  for (const name of ["herdr_relay", "herdr_handoff", "herdr_agent_start", "herdr_agent_list"]) {
    if (!names.includes(name)) throw new Error(`Missing ${name}`);
  }
  if (names.length !== 10) throw new Error(`Unexpected tool count ${names.length}`);
  console.log(`MCP initialization and discovery passed: ${names.length} scoped tools.`);
} catch (error) { console.error(error.message); process.exitCode = 1; }
finally { lines.close(); child.stdin.end(); child.kill("SIGTERM"); }
