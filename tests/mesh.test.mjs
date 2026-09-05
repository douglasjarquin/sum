import test from 'node:test';
import assert from 'node:assert/strict';
import { handlers } from '../patches/herdr-mesh/commands.mjs';
function fixture(status = 'idle', fail = false) {
  const calls = [];
  const api = handlers(async (args, options) => {
    calls.push({ args, options });
    if (args[0] === 'agent' && args[1] === 'get') return { json: { result: { agent: { agent_status: status } } }, stdout: '' };
    if (fail && args.includes('--wait')) throw new Error('timeout');
    return { json: { result: { ok: true } }, stdout: 'worker output\n' };
  });
  return { api, calls };
}
test('relay uses native agent prompt, not send plus raw Enter', async () => {
  const { api, calls } = fixture();
  const r = await api.herdr_relay({ target: 'w1:p1', message: '--session malicious' });
  assert.deepEqual(calls[1].args, ['agent', 'prompt', 'w1:p1', 'sum message:\n--session malicious']);
  assert.match(r.content[0].text, /submitted-not-acknowledged/);
});
for (const status of ['working', 'blocked', 'unknown']) {
  test(`relay refuses ${status} without injecting`, async () => {
    const { api, calls } = fixture(status);
    await assert.rejects(api.herdr_relay({ target: 'w1:p1', message: 'hello' }), /do not inject/);
    assert.equal(calls.length, 1);
  });
}
test('wait uses --until and a bounded timeout', async () => {
  const { api, calls } = fixture();
  await api.herdr_agent_wait({ target: 'reviewer', status: 'done', timeout_ms: 999999 });
  assert.deepEqual(calls[0].args, ['agent', 'wait', 'reviewer', '--until', 'done', '--timeout', '60000']);
  assert.equal(calls[0].options.timeoutMs, 65000);
});
test('handoff uses one native prompt/wait operation', async () => {
  const { api, calls } = fixture();
  const r = await api.herdr_handoff({ target: 'reviewer', message: 'review', timeout_ms: 5000 });
  assert.ok(calls[1].args.includes('--wait'));
  assert.ok(calls[1].args.includes('--until'));
  assert.match(r.content[0].text, /NOT proof of task completion/);
});
test('failed handoff is an error, not a successful stale screen read', async () => {
  const { api, calls } = fixture('idle', true);
  const r = await api.herdr_handoff({ target: 'reviewer', message: 'review' });
  assert.equal(r.isError, true);
  assert.match(r.content[0].text, /may already have been submitted/);
  assert.equal(calls.length, 2);
});
test('start requires existing pane and native kind', async () => {
  const { api, calls } = fixture();
  await api.herdr_agent_start({ name: 'reviewer', kind: 'codex', pane_id: 'w1:p2', args: ['-m', 'chosen-model'] });
  assert.deepEqual(calls[0].args, ['agent', 'start', 'reviewer', '--kind', 'codex', '--pane', 'w1:p2', '--timeout', '30000', '--', '-m', 'chosen-model']);
});
test('visible reads are bounded and passive', async () => {
  const { api, calls } = fixture();
  await api.herdr_agent_read({ target: 'reviewer' });
  assert.deepEqual(calls[0].args, ['agent', 'read', 'reviewer', '--source', 'visible', '--lines', '80', '--format', 'text']);
});
test('the curated surface excludes destructive lifecycle tools', () => {
  const { api } = fixture();
  assert.equal(Object.keys(api).length, 10);
  assert.ok(!('herdr_session_delete' in api));
  assert.ok(!('herdr_pane_close' in api));
  assert.ok(!('herdr_agent_send' in api));
});
