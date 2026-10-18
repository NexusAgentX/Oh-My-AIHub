// Isolated official-client experiment, not an upstream capability test.
// Usage: node codex-bootstrap.mjs /absolute/path/to/codex
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import https from 'node:https';
import { tmpdir } from 'node:os';
import path from 'node:path';
import readline from 'node:readline';

const binary = process.argv[2];
assert(binary && path.isAbsolute(binary), 'supply an absolute official CLI path');
const version = spawnSync(binary, ['--version'], { encoding: 'utf8', timeout: 10000, env: { PATH: process.env.PATH } });
assert.equal(version.status, 0, 'CLI version command failed');
assert.equal(version.stdout.trim(), 'codex-cli 0.163.0', 'this experiment pins 0.163.0');
let cancelled = false;
let abortExperiment = () => {};
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
  cancelled = true;
  process.exitCode = signal === 'SIGINT' ? 130 : 143;
  abortExperiment();
});

async function experiment(mode) {
  const root = await mkdtemp(path.join(tmpdir(), 'aihub-codex-bootstrap-'));
  const configDir = path.join(root, 'config');
  const workspace = path.join(root, 'workspace');
  await mkdir(configDir);
  await mkdir(workspace);
  const openssl = args => {
    const result = spawnSync('openssl', args, { cwd: root, stdio: 'ignore', timeout: 15000 });
    assert.equal(result.status, 0, 'fixture certificate generation failed');
  };
  openssl(['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=AIHubFixtureCA',
    '-addext', 'basicConstraints=critical,CA:TRUE', '-keyout', 'ca.key', '-out', 'ca.pem']);
  openssl(['req', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN=127.0.0.1', '-keyout', 'server.key', '-out', 'server.csr']);
  await writeFile(path.join(root, 'server.ext'), 'subjectAltName=IP:127.0.0.1,DNS:localhost\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n');
  openssl(['x509', '-req', '-in', 'server.csr', '-CA', 'ca.pem', '-CAkey', 'ca.key', '-CAcreateserial',
    '-days', '1', '-extfile', 'server.ext', '-out', 'server.pem']);
  const observations = [];
  const key = 'aihub-synthetic-platform-key';
  const server = https.createServer({ key: await readFile(path.join(root, 'server.key')),
    cert: await readFile(path.join(root, 'server.pem')) }, async (req, res) => {
    const chunks = [];
    let bytes = 0;
    for await (const chunk of req) {
      bytes += chunk.length;
      if (bytes > 1024 * 1024) { res.writeHead(413).end(); return; }
      chunks.push(chunk);
    }
    const body = chunks.length ? JSON.parse(Buffer.concat(chunks)) : null;
    observations.push({ method: req.method, path: req.url.split('?')[0],
      authenticated: req.headers.authorization === `Bearer ${key}`,
      inputTypes: Array.isArray(body?.input) ? [...new Set(body.input.map(x => x.type ?? x.role))] : [],
      encryptedArguments: req.headers['x-openai-encrypted-tool-arguments'] === 'true' });
    const send = (status, data) => {
      res.writeHead(status, { 'content-type': 'application/json' });
      res.end(JSON.stringify(data));
    };
    if (req.headers.authorization !== `Bearer ${key}`) {
      send(401, { error: { message: 'fixture credential required', type: 'authentication_error' } });
      return;
    }
    if (req.url.startsWith('/v1/user-auth-credential/whoami')) {
      // Synthetic platform identities and an unknown plan, never Pro or an upstream identity.
      send(200, { email: null, chatgpt_user_id: 'aihub-user-fixture',
        chatgpt_account_id: 'aihub-account-fixture', chatgpt_plan_type: 'aihub',
        chatgpt_account_is_fedramp: false });
    } else if (req.url.startsWith('/backend-api/wham/accounts/check')) {
      send(200, { accounts: [{ id: 'aihub-account-fixture', plan_type: null,
        workspace_backend_origin: 'NO_CONSTRAINT', account_routing_override: 'NO_CONSTRAINT',
        structure: 'personal' }], account_ordering: ['aihub-account-fixture'] });
    } else if (req.url.endsWith('/alpha/notes/v2/thread_hint')) {
      send(200, { text: '' });
    } else if (req.url.endsWith('/responses')) {
      res.writeHead(200, { 'content-type': 'text/event-stream' });
      const item = { id: 'msg_fixture', type: 'message', role: 'assistant', status: 'completed',
        content: [{ type: 'output_text', text: 'fixture-ok', annotations: [] }] };
      const response = { id: 'resp_fixture', object: 'response', created_at: 1,
        status: 'completed', output: [item], usage: { input_tokens: 8, output_tokens: 3, total_tokens: 11 } };
      for (const event of [
        { type: 'response.created', response: { ...response, status: 'in_progress', output: [] } },
        { type: 'response.output_item.added', output_index: 0, item: { ...item, status: 'in_progress', content: [] } },
        { type: 'response.output_text.delta', item_id: item.id, output_index: 0, content_index: 0, delta: 'fixture-ok' },
        { type: 'response.output_item.done', output_index: 0, item },
        { type: 'response.completed', response },
      ]) res.write(`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
      res.end();
    } else send(404, { error: { message: 'fixture endpoint absent', type: 'invalid_request_error' } });
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `https://127.0.0.1:${server.address().port}`;
  await writeFile(path.join(configDir, 'config.toml'), `
model = "gpt-6.1-sol"
model_provider = "openai"
openai_base_url = "${base}/backend-api/codex"
chatgpt_base_url = "${base}/backend-api/"
web_search = "disabled"
approval_policy = "never"
sandbox_mode = "read-only"
cli_auth_credentials_store = "file"
[analytics]
enabled = false
[features]
plugins = false
apps = false
shell_tool = false
enable_request_compression = false
[features.token_budget]
enabled = true
use_history_notes_extension = true
`);
  await writeFile(path.join(configDir, 'auth.json'), JSON.stringify(mode === 'api-key'
    ? { OPENAI_API_KEY: key }
    : { personal_access_token: key }));
  const child = spawn(binary, ['app-server'], { cwd: workspace, detached: true,
    env: { PATH: process.env.PATH, CODEX_HOME: configDir, CODEX_AUTHAPI_BASE_URL: base,
      NO_PROXY: '127.0.0.1,localhost', RUST_LOG: 'off',
      SSL_CERT_FILE: path.join(root, 'ca.pem'), NODE_EXTRA_CA_CERTS: path.join(root, 'ca.pem'),
      HTTP_PROXY: base, HTTPS_PROXY: base, ALL_PROXY: base,
      http_proxy: base, https_proxy: base, all_proxy: base,
      GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1',
      GIT_TERMINAL_PROMPT: '0', GIT_ASKPASS: 'false', GIT_SSH_COMMAND: 'false' },
    stdio: ['pipe', 'pipe', 'pipe'] });
  const pending = new Map();
  const notifications = [];
  let id = 0;
  let outputBytes = 0;
  let wake = () => {};
  // Discard raw diagnostics; neither credentials nor response/prompt bodies are artifacts.
  child.stderr.on('data', () => {});
  const lines = readline.createInterface({ input: child.stdout });
  lines.on('line', line => {
    outputBytes += Buffer.byteLength(line);
    if (outputBytes > 2 * 1024 * 1024) { child.kill('SIGTERM'); return; }
    let msg;
    try { msg = JSON.parse(line); } catch { return; }
    if (pending.has(msg.id)) {
      const waiter = pending.get(msg.id);
      pending.delete(msg.id);
      msg.error ? waiter.reject(new Error(`RPC ${waiter.method}: ${msg.error.code}`)) : waiter.resolve(msg.result);
    } else if (msg.method) { notifications.push(msg); wake(); }
  });
  const killGroup = signal => { try { process.kill(-child.pid, signal); } catch (error) { if (error.code !== 'ESRCH') throw error; } };
  const stop = () => killGroup('SIGTERM');
  abortExperiment = stop;
  const closed = new Promise(resolve => child.once('close', resolve));
  child.once('close', () => {
    for (const waiter of pending.values()) waiter.reject(new Error('isolated CLI exited'));
    pending.clear();
  });
  const deadline = setTimeout(stop, 45000);
  const call = (method, params) => new Promise((resolve, reject) => {
    const requestId = ++id;
    const timer = setTimeout(() => { pending.delete(requestId); reject(new Error(`RPC timeout: ${method}`)); }, 15000);
    pending.set(requestId, { method, resolve: x => { clearTimeout(timer); resolve(x); },
      reject: x => { clearTimeout(timer); reject(x); } });
    child.stdin.write(JSON.stringify({ id: requestId, method, params }) + '\n');
  });
  try {
    await call('initialize', { clientInfo: { name: 'aihub-contract-prototype', version: '1' },
      capabilities: { experimentalApi: true } });
    child.stdin.write(JSON.stringify({ method: 'initialized' }) + '\n');
    const account = await call('account/read', {});
    const thread = await call('thread/start', { model: 'gpt-6.1-sol', cwd: workspace,
      approvalPolicy: 'never', sandbox: 'read-only',
      baseInstructions: 'Isolated synthetic protocol fixture. Do not use any tools.' });
    await call('turn/start', { threadId: thread.thread.id, input: [{ type: 'text', text: 'Reply fixture-ok.' }] });
    const end = Date.now() + 15000;
    while (!notifications.some(x => x.method === 'turn/completed')) {
      assert(Date.now() < end, 'turn did not complete');
      await new Promise(resolve => { const t = setTimeout(resolve, 100); wake = () => { clearTimeout(t); resolve(); }; });
    }
    const status = notifications.find(x => x.method === 'turn/completed').params.turn.status;
    assert.equal(status, 'completed', 'synthetic inference did not complete');
    assert(observations.some(x => x.path.endsWith('/responses') && x.authenticated));
    const notesEnabled = observations.some(x => x.path.endsWith('/alpha/notes/v2/thread_hint'));
    assert.equal(notesEnabled, mode === 'synthetic-platform-pat', 'notes activation differs from the fixed-version contract');
    assert.notEqual(account.account?.planType, 'pro', 'fixture must not claim Pro');
    return { evidence: 'official_cli_with_synthetic_backend', version: version.stdout.trim(), mode,
      accountType: account.account?.type ?? null, completed: true,
      planType: account.account?.planType ?? null, notesEnabled,
      observations };
  } finally {
    clearTimeout(deadline);
    stop();
    await closed;
    killGroup('SIGKILL');
    abortExperiment = () => {};
    lines.close();
    await new Promise(resolve => server.close(resolve));
    await rm(root, { recursive: true, force: true, maxRetries: 3, retryDelay: 50 });
  }
}

for (const mode of ['api-key', 'synthetic-platform-pat']) {
  if (cancelled) break;
  const result = await experiment(mode);
  console.log(JSON.stringify(result));
}
