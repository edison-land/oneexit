// TempRoom desktop frontend. The Go service is the source of truth; this file
// only renders what /api/session reports and never invents state.
let currentSession = null;
let pollTimer = null;
let mutationBusy = false;
let uiEpoch = 0;
let selectedProxy = '127.0.0.1:7890';
let cameraText = 'Detecting...';
let relay = { mode: 'public', customWorkerUrl: '' }; // saved choice
let relayChoice = 'public'; // what the radio shows

const $ = (id) => document.getElementById(id);

document.addEventListener('DOMContentLoaded', () => {
  refreshProxies();
  loadRelay();
  startPolling();
  $('input-worker-url').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') saveCustomRelay();
  });
  $('input-room-key').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') joinRoom();
  });
});

function switchRole(role) {
  document.querySelectorAll('.switch-btn').forEach(b => b.classList.remove('active'));
  document.querySelectorAll('.panel').forEach(p => p.classList.remove('active'));
  const req = role === 'REQUESTER';
  $(req ? 'role-tab-requester' : 'role-tab-operator').classList.add('active');
  $(req ? 'panel-requester' : 'panel-operator').classList.add('active');
}

async function api(path, options = {}) {
  const res = await fetch('/api' + path, { headers: { 'Content-Type': 'application/json' }, ...options });
  if (!res.ok) {
    const text = (await res.text()).trim();
    throw new Error(text || res.statusText);
  }
  return res.json();
}

function startPolling() {
  if (pollTimer) clearTimeout(pollTimer);
  const poll = async () => {
    const epoch = uiEpoch;
    try {
      if (!mutationBusy) {
        const { session, ended } = await api('/session');
        if (!mutationBusy && epoch === uiEpoch) {
          if (session) renderSession(session);
          else if (currentSession) showEnded(ended);
        }
        const status = await api('/status');
        if (!mutationBusy && epoch === uiEpoch) updateStatus(status);
      }
    } catch (e) {
      $('status-text').textContent = 'Reconnecting…';
    } finally {
      pollTimer = setTimeout(poll, 1200);
    }
  };
  poll();
}

async function mutate(fn) {
  if (mutationBusy) return;
  mutationBusy = true; uiEpoch++;
  try { await fn(); } finally { mutationBusy = false; uiEpoch++; }
}

// ---------- idle ----------

async function refreshProxies() {
  const list = $('proxy-list');
  list.innerHTML = '<div class="proxy-item"><span class="proxy-meta">Scanning local proxies…</span></div>';
  let results = [];
  try { results = await api('/proxy/probe') || []; } catch (e) { results = []; }
  list.innerHTML = '';
  if (results.length === 0) {
    const item = document.createElement('div');
    item.className = 'proxy-item selected';
    item.innerHTML = '<span class="proxy-address">127.0.0.1:7890</span><span class="proxy-meta">Not detected — start Clash/mihomo, then Scan</span>';
    list.appendChild(item);
    selectedProxy = '127.0.0.1:7890';
    return;
  }
  results.forEach((res, i) => {
    const item = document.createElement('div');
    item.className = 'proxy-item' + (i === 0 ? ' selected' : '');
    const addr = document.createElement('span');
    addr.className = 'proxy-address';
    addr.textContent = res.address;
    const meta = document.createElement('span');
    meta.className = 'proxy-meta';
    meta.textContent = `${res.latencyMs}ms${res.exitIp ? ' · exit ' + res.exitIp : ' · no internet'}`;
    item.append(addr, meta);
    item.onclick = () => {
      document.querySelectorAll('.proxy-item').forEach(el => el.classList.remove('selected'));
      item.classList.add('selected');
      selectedProxy = res.address;
    };
    list.appendChild(item);
  });
  selectedProxy = results[0].address;
}

// ---------- relay ----------

function setRelayState(state, text) {
  $('relay-dot').className = 'status-indicator ' + ({ ok: 'active', bad: 'failed', busy: 'waiting' }[state] || 'ready');
  $('relay-state-text').textContent = text;
}

function renderRelay() {
  $('relay-opt-public').classList.toggle('selected', relayChoice === 'public');
  $('relay-opt-custom').classList.toggle('selected', relayChoice === 'custom');
  $('relay-opt-public').setAttribute('aria-checked', relayChoice === 'public');
  $('relay-opt-custom').setAttribute('aria-checked', relayChoice === 'custom');
  show('input-worker-url', relayChoice === 'custom');
}

function showHealth(h) {
  if (h && h.ok) setRelayState('ok', `Connected · ${h.latencyMs}ms`);
  else setRelayState('bad', 'Offline');
  $('relay-state').title = (h && h.error) || (h && h.url) || '';
}

async function loadRelay() {
  try { relay = await api('/relay'); } catch (e) { /* keep defaults */ }
  relayChoice = relay.publicAvailable ? relay.mode : 'custom';
  $('relay-opt-public').disabled = !relay.publicAvailable;
  $('relay-opt-public').title = relay.publicAvailable ? '' : 'This build has no default relay';
  $('input-worker-url').value = relay.customWorkerUrl || '';
  renderRelay();
  if (!relay.publicAvailable && !relay.customWorkerUrl) { setRelayState('', 'Enter your Worker URL'); return; }
  setRelayState('busy', 'Checking…');
  try { showHealth(await api('/relay/check', { method: 'POST', body: '{}' })); } catch (e) { showHealth(null); }
}

async function applyRelay(mode, url) {
  setRelayState('busy', 'Checking…');
  let res;
  try { res = await api('/relay', { method: 'POST', body: JSON.stringify({ mode, url }) }); }
  catch (e) { res = { config: relay, health: { ok: false, error: e.message }, error: e.message }; }
  relay = res.config || relay;
  showHealth(res.health);
  $('input-worker-url').classList.toggle('invalid', !!res.error && mode === 'custom');
  return !res.error;
}

async function chooseRelay(mode) {
  if (mode === 'public' && !relay.publicAvailable) return;
  relayChoice = mode;
  renderRelay();
  if (mode === 'public') {
    $('input-worker-url').classList.remove('invalid');
    await applyRelay('public', '');
  } else if ($('input-worker-url').value.trim()) {
    await saveCustomRelay();
  } else {
    setRelayState('', 'Enter your Worker URL');
    $('input-worker-url').focus();
  }
}

async function saveCustomRelay() {
  const url = $('input-worker-url').value.trim();
  if (!url) { $('input-worker-url').focus(); return false; }
  const ok = await applyRelay('custom', url);
  if (ok) $('input-worker-url').value = relay.customWorkerUrl;
  return ok;
}

// Make sure what the radio shows is what the server will use.
async function relayReady() {
  if (relayChoice === 'public') return relay.mode === 'public' || applyRelay('public', '');
  const typed = $('input-worker-url').value.trim();
  if (relay.mode === 'custom' && typed === relay.customWorkerUrl) return true;
  return saveCustomRelay();
}

async function createRoom() {
  const btn = document.querySelector('#panel-requester .btn-primary');
  await mutate(async () => {
    if (!(await relayReady())) {
      alert('Your Cloudflare Worker did not answer. Check the URL (press Enter to test it) or switch to Default Relay.');
      return;
    }
    btn.disabled = true; btn.textContent = 'Checking proxy…';
    try {
      renderSession(await api('/room/create', { method: 'POST', body: JSON.stringify({ proxy: selectedProxy }) }));
    } catch (err) {
      alert('Could not create room:\n' + err.message);
    } finally {
      btn.disabled = false; btn.textContent = 'Create Room';
    }
  });
}

async function joinRoom() {
  const input = $('input-room-key');
  const invite = input.value.trim();
  if (!invite) { input.focus(); return; }
  const btn = document.querySelector('#panel-operator .btn-primary');
  await mutate(async () => {
    btn.disabled = true; btn.textContent = 'Joining…';
    try {
      renderSession(await api('/room/join', { method: 'POST', body: JSON.stringify({ invite }) }));
    } catch (err) {
      alert('Could not join:\n' + err.message);
    } finally {
      btn.disabled = false; btn.textContent = 'Join Room';
    }
  });
}

// ---------- session ----------

async function approveJoin() {
  await mutate(async () => {
    try { renderSession(await api('/room/approve', { method: 'POST' })); }
    catch (err) { alert('Approval failed:\n' + err.message); }
  });
}

async function rejectJoin() {
  // Declining closes the room so the partner gets a definite answer.
  await leaveRoom();
}

async function leaveRoom() {
  await mutate(async () => {
    $('approval-modal').classList.add('hidden');
    try {
      const results = await api('/leave', { method: 'POST' });
      showEnded({ reason: 'You ended the session.', results });
    } catch (err) {
      alert('Error while ending the session:\n' + err.message);
    }
  });
}

async function toggleChrome() {
  const btn = $('btn-chrome');
  btn.disabled = true;
  try {
    if (currentSession && currentSession.chromeRunning) {
      await api('/chrome/close', { method: 'POST' });
    } else {
      await api('/chrome/launch', { method: 'POST', body: JSON.stringify({ url: 'https://www.google.com' }) });
    }
    renderSession((await api('/session')).session);
  } catch (err) {
    alert('Browser: ' + err.message);
  } finally {
    btn.disabled = false;
    if (currentSession) renderSession(currentSession);
  }
}

function copyInvite() {
  if (!currentSession || !currentSession.invite) return;
  navigator.clipboard.writeText(currentSession.invite).then(() => {
    $('copy-badge').textContent = 'Copied';
    setTimeout(() => { $('copy-badge').textContent = 'Copy invite'; }, 1500);
  });
}

function setNote(text, level) {
  const note = $('net-note');
  if (!text) { note.className = 'net-note hidden'; return; }
  note.className = 'net-note' + (level ? ' ' + level : '');
  note.textContent = text;
}

function show(id, visible) { $(id).classList.toggle('hidden', !visible); }

function renderSession(session) {
  if (!session) return;
  currentSession = session;
  showView('view-session');
  const host = session.role === 'REQUESTER';
  const st = session.state;
  const net = session.network || {};

  $('display-room-key').textContent = session.roomKey || '----';
  show('copy-badge', host);
  $('key-wrap').style.cursor = host ? 'pointer' : 'default';
  $('key-hint').textContent = host ? 'Copy sends the full invite (room key + encryption secret). Share it only with your partner.' : '';

  // Waiting card
  const waiting = (host && st === 'WAITING') || (!host && st === 'PENDING_APPROVAL');
  show('waiting-card', waiting);
  if (host) {
    $('waiting-title').textContent = 'Waiting for partner';
    $('waiting-desc').textContent = 'Send the full invite code to your partner.';
  } else {
    $('waiting-title').textContent = 'Waiting for host approval';
    $('waiting-desc').textContent = 'The host must approve your request.';
  }

  // Join request modal (host only, only while pending)
  show('approval-modal', host && st === 'PENDING_APPROVAL');
  if (host) $('modal-scope').textContent = `Your proxy ${net.proxyAddress || ''} — nothing else on this Mac`;

  // Host sharing card
  show('peer-active-card', host && st === 'ACTIVE');
  if (host && st === 'ACTIVE') {
    $('peer-title').textContent = net.relayOnline ? 'Sharing active' : 'Sharing paused';
    $('peer-dot').className = 'status-indicator ' + (net.relayOnline ? 'active' : 'waiting');
    $('peer-desc').textContent = net.relayOnline
      ? `Partner traffic exits through ${net.proxyAddress} · ${net.streams || 0} open connection(s)`
      : 'Relay link is down; partner traffic is blocked until it reconnects.';
  }

  // Metrics
  if (host) {
    $('metric-exit-label').textContent = 'Your Proxy Exit';
    $('metric-exit-ip').textContent = net.expectedExitIp || '—';
    setTunnel(st !== 'ACTIVE' ? ['Not started', ''] : net.relayOnline ? ['Relay connected', 'status-connected'] : ['Reconnecting', 'status-connecting']);
  } else {
    $('metric-exit-label').textContent = 'Browser Exit';
    $('metric-exit-ip').textContent = net.state === 'VERIFIED' ? `${net.exitIp} (verified)` : (net.expectedExitIp ? `expect ${net.expectedExitIp}` : '—');
    const map = {
      CONNECTING: ['Verifying…', 'status-connecting'],
      VERIFIED: ['Encrypted · verified', 'status-connected'],
      FAILED: ['Blocked', 'status-disconnected'],
    };
    setTunnel(st !== 'ACTIVE' ? ['Not started', ''] : (map[net.state] || ['Starting…', 'status-connecting']));
  }
  $('metric-camera').textContent = cameraText;
  $('metric-relay').textContent = session.relay ? session.relay : 'Default';

  // Notes: errors beat warnings beat sync problems
  if (net.message && (net.state === 'FAILED' || (host && st === 'ACTIVE' && !net.relayOnline))) setNote(net.message, 'error');
  else if (net.message) setNote(net.message, '');
  else if (net.warning) setNote(net.warning, 'warn');
  else if (session.syncError) setNote('Signaling interrupted, retrying… Access is revoked automatically if this lasts over a minute.', 'warn');
  else setNote('', '');

  // Operator browser
  show('operator-tools', !host && st === 'ACTIVE');
  const btn = $('btn-chrome');
  if (session.chromeRunning) {
    $('chrome-status-text').textContent = 'Running · isolated profile · only via tunnel';
    btn.textContent = 'Close Chrome';
    btn.classList.replace('btn-secondary', 'btn-danger');
    btn.disabled = false;
  } else {
    const ok = net.state === 'VERIFIED';
    $('chrome-status-text').textContent = ok ? 'Ready · clean temporary profile' : 'Blocked until the network is verified';
    btn.textContent = 'Launch Chrome';
    btn.classList.replace('btn-danger', 'btn-secondary');
    btn.disabled = !ok;
  }
  updateTopbar();
}

function setTunnel([text, cls]) {
  const el = $('metric-tunnel');
  el.textContent = text;
  el.className = 'metric-val ' + cls;
}

function updateTopbar() {
  const dot = $('status-dot');
  const text = $('status-text');
  const s = currentSession;
  if (!s) { text.textContent = 'READY'; dot.className = 'status-indicator ready'; return; }
  const net = s.network || {};
  let label = s.state, cls = 'waiting';
  if (s.syncError) { label = 'Signaling interrupted'; }
  else if (s.state === 'WAITING') label = 'Waiting';
  else if (s.state === 'PENDING_APPROVAL') label = s.role === 'REQUESTER' ? 'Join request' : 'Awaiting approval';
  else if (s.state === 'ACTIVE' && s.role === 'REQUESTER') {
    label = net.relayOnline ? 'Sharing' : 'Sharing paused';
    cls = net.relayOnline ? 'active' : 'waiting';
  } else if (s.state === 'ACTIVE') {
    if (net.state === 'VERIFIED') { label = 'Connected via host'; cls = 'active'; }
    else if (net.state === 'FAILED') { label = 'Blocked'; cls = 'failed'; }
    else label = 'Verifying network';
  }
  text.textContent = label;
  dot.className = 'status-indicator ' + cls;
}

function updateStatus(status) {
  if (!status) return;
  cameraText = status.camera && status.camera.available ? (status.camera.name || 'Available') : 'None detected';
  $('metric-camera').textContent = cameraText;
  if (!currentSession) updateTopbar();
}

// ---------- ended ----------

function showEnded(ended) {
  currentSession = null;
  $('approval-modal').classList.add('hidden');
  setNote('', '');
  showView('view-cleanup');
  $('end-reason').textContent = (ended && ended.reason) || 'The session ended.';
  const container = $('cleanup-steps');
  container.innerHTML = '';
  ((ended && ended.results) || []).forEach(res => {
    const row = document.createElement('div');
    row.className = 'step-item';
    const name = document.createElement('span');
    name.className = 'step-name';
    name.textContent = res.step + (res.error ? ` — ${res.error}` : '');
    const st = document.createElement('span');
    st.className = 'step-status ' + (res.skipped ? 'skipped' : res.success ? '' : 'failed');
    st.textContent = res.skipped ? '— Not needed' : res.success ? '✓ Done' : '✗ Failed';
    row.append(name, st);
    container.appendChild(row);
  });
  updateTopbar();
}

function resetToIdle() {
  currentSession = null;
  $('approval-modal').classList.add('hidden');
  $('input-room-key').value = '';
  showView('view-idle');
  updateTopbar();
}

function showView(viewId) {
  document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
  const target = $(viewId);
  if (target) target.classList.add('active');
}
