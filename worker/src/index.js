// TempRoom private relay for Cloudflare Workers.
//
// It speaks the same protocol as cmd/temproom-server (protocol 2), so the
// desktop app treats it exactly like the public relay. Each room lives in its
// own Durable Object, purely in memory: nothing is written to storage, nothing
// is logged, and the room disappears the moment it is closed, expires, or the
// object is evicted. Browser bytes are TLS 1.3 ciphertext end to end between
// A and B; this Worker only shuttles opaque frames between two WebSockets.

const PROTOCOL = 2;
const WAITING_TTL = 30 * 60 * 1000; // unapproved rooms
const ACTIVE_TTL = 24 * 60 * 60 * 1000; // sliding window while active peers poll
const LEASE_TTL = 45 * 1000; // a side that stops polling this long is gone
const SWEEP_EVERY = 5 * 1000;
const KEEPALIVE_EVERY = 25 * 1000; // keeps idle control/hint sockets open
const STREAM_WAIT = 10 * 1000; // B waits this long for A to dial back
const MAX_STREAMS = 256;
const MAX_EARLY_BYTES = 1 << 20; // B bytes buffered before A's side arrives
const KEY_RE = /^[A-Z2-7]{4}-[A-Z2-7]{4}$/;
const B32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';

const json = (v, status = 200) =>
  new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' } });
const fail = (msg, status) => new Response(msg + '\n', { status, headers: { 'Content-Type': 'text/plain' } });

// 5 bytes -> "XXXX-XXXX" (same alphabet and shape as the Go server).
function keyFromBytes(b) {
  let bits = 0n;
  for (const x of b.slice(0, 5)) bits = (bits << 8n) | BigInt(x);
  let s = '';
  for (let i = 7; i >= 0; i--) s += B32[Number((bits >> BigInt(i * 5)) & 31n)];
  return s.slice(0, 4) + '-' + s.slice(4);
}
const randomKey = () => keyFromBytes(crypto.getRandomValues(new Uint8Array(5)));
// A create retry carries the same requestId, so it must land on the same room.
async function keyForRequest(requestId) {
  const d = await crypto.subtle.digest('SHA-256', new TextEncoder().encode('temproom-create\0' + requestId));
  return keyFromBytes(new Uint8Array(d));
}
function randHex(n) {
  return [...crypto.getRandomValues(new Uint8Array(n))].map((x) => x.toString(16).padStart(2, '0')).join('');
}
function sameToken(a, b) {
  if (!a || !b || a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}
const normKey = (k) => (k || '').trim().toUpperCase();
const bearer = (req) => (req.headers.get('Authorization') || '').replace(/^Bearer /, '');

async function readBody(req) {
  const text = await req.text();
  if (text.length > 4096) return null;
  if (!text.trim()) return {};
  try {
    const v = JSON.parse(text);
    return v && typeof v === 'object' ? v : null;
  } catch {
    return null;
  }
}

const roomStub = (env, key) => env.ROOMS.get(env.ROOMS.idFromName(key));
function toRoom(env, key, path, init) {
  return roomStub(env, key).fetch('https://room' + path, init);
}

export default {
  async fetch(req, env) {
    const url = new URL(req.url);
    const path = url.pathname;

    if (path === '/health') {
      return json({ status: 'ok', worker: true, protocol: PROTOCOL, time: new Date().toISOString() });
    }
    if (path === '/') return fail('TempRoom private relay', 200);

    if (path.startsWith('/api/v1/rooms/') && path !== '/api/v1/rooms/status') {
      if (req.method !== 'POST') return fail('method not allowed', 405);
      const body = await readBody(req);
      if (!body) return fail('invalid request', 400);
      const auth = req.headers.get('Authorization') || '';
      const send = (key) =>
        toRoom(env, key, path, { method: 'POST', headers: { Authorization: auth, 'X-Room-Key': key }, body: JSON.stringify(body) });

      if (path === '/api/v1/rooms/create') {
        const exitIp = String(body.exitIp || '');
        const requestId = String(body.requestId || '');
        if (exitIp.length > 64 || requestId.length > 64) return fail('invalid request', 400);
        let key = requestId ? await keyForRequest(requestId) : randomKey();
        for (let i = 0; i < 4; i++) {
          const res = await send(key);
          if (res.status !== 409) return res;
          key = randomKey(); // collision with someone else's room
        }
        return fail('room allocation failed', 503);
      }
      const key = normKey(body.roomKey);
      if (!KEY_RE.test(key)) return fail('room not found or expired', 404);
      return send(key);
    }

    if (path === '/api/v1/rooms/status' || path === '/ws' || path.startsWith('/ws/relay/')) {
      const key = normKey(url.searchParams.get('key'));
      if (!KEY_RE.test(key)) return fail('room not found or expired', 404);
      const headers = new Headers(req.headers);
      headers.set('X-Room-Key', key);
      return roomStub(env, key).fetch(new Request('https://room' + path + url.search, { method: req.method, headers }));
    }

    return fail('not found', 404);
  },
};

function safeSend(ws, data) {
  try {
    ws.send(data);
    return true;
  } catch {
    return false;
  }
}
function safeClose(ws, code = 1000, reason = '') {
  if (!ws) return;
  try {
    ws.close(code, reason);
  } catch {
    /* already closed */
  }
}
function upgrade() {
  const pair = new WebSocketPair();
  const [client, server] = Object.values(pair);
  server.accept();
  return { server, response: new Response(null, { status: 101, webSocket: client }) };
}

// One room. All state is plain JS memory owned by this object.
export class Room {
  constructor(ctx) {
    this.ctx = ctx;
    this.room = null;
  }

  async fetch(req) {
    const url = new URL(req.url);
    const key = req.headers.get('X-Room-Key') || '';
    this.expireIfDue();
    switch (url.pathname) {
      case '/api/v1/rooms/create':
        return this.create(key, await req.json());
      case '/api/v1/rooms/join':
        return this.join(await req.json());
      case '/api/v1/rooms/approve':
        return this.approve(bearer(req));
      case '/api/v1/rooms/leave':
        return this.leave(bearer(req));
      case '/api/v1/rooms/status':
        return this.status(bearer(req));
      case '/ws':
        return this.hints(req);
      case '/ws/relay/provider':
        return this.provider(req);
      case '/ws/relay/connect':
        return this.connect(req);
      case '/ws/relay/accept':
        return this.accept(req, url.searchParams.get('id') || '');
    }
    return fail('not found', 404);
  }

  // ---- room lifecycle ----

  create(key, body) {
    const requestId = String(body.requestId || '');
    const r = this.room;
    if (r) {
      if (requestId && sameToken(requestId, r.createId) && r.state === 'WAITING') return json(this.view('REQUESTER', true));
      return fail('room key taken', 409);
    }
    const now = Date.now();
    this.room = {
      key,
      state: 'WAITING',
      exitIp: String(body.exitIp || ''),
      createdAt: now,
      expiresAt: now + WAITING_TTL,
      requesterToken: randHex(24),
      operatorToken: '',
      joinId: '',
      createId: requestId,
      seen: { REQUESTER: now },
      hints: new Map(), // role -> ws
      provider: null, // A's control socket
      streams: new Set(), // live stream pairs
      pending: new Map(), // stream id -> pair waiting for A
    };
    this.timer = setInterval(() => this.sweep(), SWEEP_EVERY);
    this.keepalive = setInterval(() => this.ping(), KEEPALIVE_EVERY);
    return json(this.view('REQUESTER', true));
  }

  join(body) {
    const r = this.room;
    if (!r) return fail('room not found or expired', 404);
    const joinId = String(body.joinId || '');
    if (joinId.length < 16 || joinId.length > 64) return fail('invalid join id', 400);
    if (r.operatorToken) {
      if (!sameToken(joinId, r.joinId)) return fail('room already has an operator', 409);
    } else {
      if (r.state !== 'WAITING') return fail('invalid room state', 409);
      r.operatorToken = randHex(24);
      r.joinId = joinId;
      r.state = 'PENDING_APPROVAL';
      this.hint('join_request');
    }
    r.seen.OPERATOR = Date.now();
    return json(this.view('OPERATOR', true));
  }

  status(token) {
    const r = this.room;
    if (!r) return fail('room not found or expired', 404);
    const role = this.auth(token);
    if (!role) return fail('invalid room token', 403);
    const now = Date.now();
    r.seen[role] = now;
    if (r.state === 'ACTIVE') r.expiresAt = now + ACTIVE_TTL;
    return json(this.view(role, false));
  }

  approve(token) {
    const r = this.room;
    if (!r) return fail('room not found or expired', 404);
    if (this.auth(token) !== 'REQUESTER') return fail('invalid room token', 403);
    if (r.state === 'PENDING_APPROVAL') {
      r.state = 'ACTIVE';
      r.expiresAt = Date.now() + ACTIVE_TTL;
      this.hint('join_approved');
    } else if (r.state !== 'ACTIVE') {
      return fail('invalid room state', 409);
    }
    r.seen.REQUESTER = Date.now();
    return json(this.view('', false));
  }

  leave(token) {
    if (!this.room) return fail('room not found or expired', 404);
    if (!this.auth(token)) return fail('invalid room token', 403);
    this.remove();
    return json({ success: true });
  }

  auth(token) {
    const r = this.room;
    if (!r || !token) return '';
    if (sameToken(token, r.requesterToken)) return 'REQUESTER';
    if (r.operatorToken && sameToken(token, r.operatorToken)) return 'OPERATOR';
    return '';
  }

  view(role, withToken) {
    const r = this.room;
    const v = {
      id: 'room_' + r.key.toLowerCase(),
      roomKey: r.key,
      state: r.state,
      createdAt: new Date(r.createdAt).toISOString(),
      expiresAt: new Date(r.expiresAt).toISOString(),
      providerOnline: r.provider !== null,
      operatorJoined: r.operatorToken !== '',
    };
    if (r.exitIp) v.exitIp = r.exitIp;
    if (role) v.role = role;
    if (withToken) v.token = role === 'REQUESTER' ? r.requesterToken : r.operatorToken;
    return v;
  }

  hint(event) {
    const msg = JSON.stringify({ event, roomKey: this.room.key });
    for (const ws of this.room.hints.values()) safeSend(ws, msg);
  }

  ping() {
    const r = this.room;
    if (!r) return;
    for (const ws of r.hints.values()) safeSend(ws, '{}');
    if (r.provider) safeSend(r.provider, '{}');
  }

  expireIfDue() {
    const r = this.room;
    if (!r) return;
    const now = Date.now();
    let dead = now >= r.expiresAt;
    for (const [role, at] of Object.entries(r.seen)) {
      if (role === 'OPERATOR' && !r.operatorToken) continue;
      if (now - at > LEASE_TTL) dead = true;
    }
    if (dead) this.remove();
  }

  sweep() {
    this.expireIfDue();
  }

  // Drops the room from memory and cuts every socket immediately.
  remove() {
    const r = this.room;
    if (!r) return;
    this.hint('room_closed');
    this.room = null;
    clearInterval(this.timer);
    clearInterval(this.keepalive);
    for (const ws of r.hints.values()) safeClose(ws, 1000, 'room closed');
    safeClose(r.provider, 1000, 'room closed');
    for (const p of r.pending.values()) this.closePair(p);
    for (const p of r.streams) this.closePair(p);
    r.hints.clear();
    r.pending.clear();
    r.streams.clear();
  }

  // ---- WebSockets ----

  // Validates before upgrading, so failures are plain HTTP errors.
  gate(req, wantRole, needActive) {
    const r = this.room;
    if (!r) return { error: fail('room not found or expired', 404) };
    if (req.headers.get('Upgrade') !== 'websocket') return { error: fail('expected websocket', 426) };
    const role = this.auth(bearer(req));
    if (!role || (wantRole && role !== wantRole)) return { error: fail('invalid room token', 403) };
    if (needActive && r.state !== 'ACTIVE') return { error: fail('invalid room state', 409) };
    return { r, role };
  }

  // Hint channel: events only wake the client's poller; REST owns all state.
  hints(req) {
    const { r, role, error } = this.gate(req, '', false);
    if (error) return error;
    const { server, response } = upgrade();
    const old = r.hints.get(role);
    r.hints.set(role, server);
    safeClose(old);
    const drop = () => {
      if (this.room === r && r.hints.get(role) === server) r.hints.delete(role);
    };
    server.addEventListener('close', drop);
    server.addEventListener('error', drop);
    return response;
  }

  // A's control link: stream requests are sent as {"stream": id}.
  provider(req) {
    const { r, error } = this.gate(req, 'REQUESTER', true);
    if (error) return error;
    const { server, response } = upgrade();
    const old = r.provider;
    r.provider = server;
    safeClose(old, 1000, 'replaced');
    const drop = () => {
      if (r.provider === server) r.provider = null;
    };
    server.addEventListener('close', drop);
    server.addEventListener('error', drop);
    return response;
  }

  // One stream from B. Waits for A to dial back, then forwards ciphertext.
  connect(req) {
    const { r, error } = this.gate(req, 'OPERATOR', true);
    if (error) return error;
    if (!r.provider) return fail('host is not connected to the relay', 503);
    if (r.streams.size + r.pending.size >= MAX_STREAMS) return fail('too many streams', 429);
    const id = randHex(16);
    const { server, response } = upgrade();
    const pair = { b: server, a: null, early: [], earlyBytes: 0, timer: null };
    server.addEventListener('message', (e) => {
      if (pair.a) {
        if (!safeSend(pair.a, e.data)) this.endPair(r, pair, id);
        return;
      }
      pair.earlyBytes += typeof e.data === 'string' ? e.data.length : e.data.byteLength;
      if (pair.earlyBytes > MAX_EARLY_BYTES) return this.endPair(r, pair, id);
      pair.early.push(e.data);
    });
    server.addEventListener('close', () => this.endPair(r, pair, id));
    server.addEventListener('error', () => this.endPair(r, pair, id));
    r.pending.set(id, pair);
    pair.timer = setTimeout(() => {
      if (r.pending.get(id) === pair) this.endPair(r, pair, id);
    }, STREAM_WAIT);
    if (!safeSend(r.provider, JSON.stringify({ stream: id }))) this.endPair(r, pair, id);
    return response;
  }

  accept(req, id) {
    const { r, error } = this.gate(req, 'REQUESTER', true);
    if (error) return error;
    const pair = r.pending.get(id);
    if (!pair) return fail('unknown stream', 404);
    r.pending.delete(id);
    clearTimeout(pair.timer);
    const { server, response } = upgrade();
    pair.a = server;
    r.streams.add(pair);
    for (const data of pair.early) {
      if (!safeSend(server, data)) {
        this.endPair(r, pair, id);
        return response;
      }
    }
    pair.early = [];
    server.addEventListener('message', (e) => {
      if (!safeSend(pair.b, e.data)) this.endPair(r, pair, id);
    });
    server.addEventListener('close', () => this.endPair(r, pair, id));
    server.addEventListener('error', () => this.endPair(r, pair, id));
    return response;
  }

  endPair(r, pair, id) {
    clearTimeout(pair.timer);
    if (r.pending.get(id) === pair) r.pending.delete(id);
    r.streams.delete(pair);
    this.closePair(pair);
  }

  closePair(pair) {
    clearTimeout(pair.timer);
    pair.early = [];
    safeClose(pair.b);
    safeClose(pair.a);
  }
}
