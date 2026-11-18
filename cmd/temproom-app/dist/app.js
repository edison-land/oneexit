// TempRoom desktop frontend. The Go service is the source of truth; this file
// only renders what /api/session reports and never invents state.
let currentSession = null;
let lastEnded = null;
let lastProxies = null;
let proxiesScanning = false;
let lastHealth = null;
let relayBusy = false;
let createBusyMode = '';
let joinBusy = false;
let cameraInfo = null;
let pollTimer = null;
let mutationBusy = false;
let uiEpoch = 0;
let selectedProxy = '127.0.0.1:7890';
let relay = { mode: 'quick', customWorkerUrl: '' }; // saved choice
let relayChoice = 'quick'; // what the radio shows

const $ = (id) => document.getElementById(id);

function detectInitialLang() {
  try {
    const saved = localStorage.getItem('temproom_lang');
    if (saved === 'zh' || saved === 'en') return saved;
  } catch {}
  return (navigator.language || '').toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

let lang = detectInitialLang();

const I18N = {
  en: {
    shareTab: 'Share Network',
    joinTab: 'Join Session',
    localProxy: 'Local Proxy',
    scan: 'Scan',
    scanningProxies: 'Scanning local proxies…',
    noProxy: 'Not detected — start Clash/mihomo, then Scan',
    exitPrefix: 'exit ',
    noInternet: 'no internet',
    relayTitle: 'Relay',
    optQuick: 'Temporary',
    optPublic: 'Default',
    optCustom: 'Self-Hosted',
    workerAria: 'Self-hosted relay URL',
    workerPlaceholder: 'https://your-relay.workers.dev',
    relayZeroLogin: 'Ready · Zero login',
    relayConnected: (ms) => `Connected · ${ms}ms`,
    relayOffline: 'Offline',
    relayChecking: 'Checking…',
    relayEnterUrl: 'Enter relay URL',
    createRoom: 'Create Room',
    startingQuick: 'Starting temporary tunnel…',
    checkingProxy: 'Checking proxy…',
    inviteLabel: 'Invite Code',
    invitePlaceholder: 'Paste the full invite code',
    inviteHint: "The host's invite contains the encryption secret. Paste all of it.",
    joinRoom: 'Join Room',
    joining: 'Joining…',
    roomKeyLabel: 'ROOM KEY',
    copyInvite: 'Copy invite',
    copied: 'Copied',
    keyHintHost: 'Copy sends the full invite (room key + encryption secret). Share it only with your partner.',
    waitPartnerTitle: 'Waiting for partner',
    waitPartnerDesc: 'Send the full invite code to your partner.',
    waitApproveTitle: 'Waiting for host approval',
    waitApproveDesc: 'The host must approve your request.',
    sharingActive: 'Sharing active',
    sharingPaused: 'Sharing paused',
    sharingDescOn: (addr, n) => `Partner traffic exits through ${addr} · ${n} open connection(s)`,
    sharingDescOff: 'Relay link is down; partner traffic is blocked until it reconnects.',
    yourProxyExit: 'Your Proxy Exit',
    browserExit: 'Browser Exit',
    verifiedSuffix: (ip) => `${ip} (verified)`,
    expectPrefix: (ip) => `expect ${ip}`,
    tunnelLabel: 'Encrypted Tunnel',
    tunNotStarted: 'Not started',
    tunRelayConn: 'Relay connected',
    tunReconnecting: 'Reconnecting',
    tunVerifying: 'Verifying…',
    tunVerified: 'Encrypted · verified',
    tunBlocked: 'Blocked',
    tunStarting: 'Starting…',
    relayRowLabel: 'Relay',
    relayDefaultVal: 'Default',
    cameraLabel: 'Camera',
    camDetecting: 'Detecting...',
    camAvailable: 'Available',
    camNone: 'None detected',
    syncWarn: 'Signaling interrupted, retrying… Access is revoked automatically if this lasts over a minute.',
    tempChrome: 'Temporary Chrome',
    chromeRunning: 'Running · isolated profile · only via tunnel',
    chromeReady: 'Ready · clean temporary profile',
    chromeBlocked: 'Blocked until the network is verified',
    launchChrome: 'Launch Chrome',
    closeChrome: 'Close Chrome',
    endSession: 'End Session',
    sessionEndedTitle: 'Session Ended',
    defaultEndReason: 'The session ended.',
    stepSkipped: '— Not needed',
    stepDone: '✓ Done',
    stepFailed: '✗ Failed',
    newSession: 'New Session',
    modalTitle: 'Join Request',
    modalBody: 'Someone used your invite and wants to browse through your proxy. Approve only if this is the person you sent it to.',
    modalScopeLabel: 'They get',
    modalScopeVal: (addr) => `Your proxy ${addr} — nothing else on this Mac`,
    declineBtn: 'Decline & close room',
    approveBtn: 'Approve',
    topReady: 'READY',
    topReconn: 'Reconnecting…',
    topSigInt: 'Signaling interrupted',
    topWaiting: 'Waiting',
    topJoinReq: 'Join request',
    topAwaitApp: 'Awaiting approval',
    topSharing: 'Sharing',
    topPaused: 'Sharing paused',
    topConnHost: 'Connected via host',
    topBlocked: 'Blocked',
    topVerifying: 'Verifying network',
    alertRelayFail: 'Your self-hosted relay did not answer. Check the URL (press Enter to test it) or switch to Temporary.',
    alertCreateFail: 'Could not create room:\n',
    alertJoinFail: 'Could not join:\n',
    alertApproveFail: 'Approval failed:\n',
    alertLeaveFail: 'Error while ending the session:\n',
    alertBrowser: 'Browser: ',
    youEnded: 'You ended the session.',
  },
  zh: {
    shareTab: '共享网络',
    joinTab: '加入房间',
    localProxy: '本地代理',
    scan: '扫描',
    scanningProxies: '正在扫描本地代理…',
    noProxy: '未检测到 — 请先启动 Clash / mihomo 后点扫描',
    exitPrefix: '出口 ',
    noInternet: '无外网',
    relayTitle: '中继通道',
    optQuick: '临时',
    optPublic: '默认',
    optCustom: '自建',
    workerAria: '自建中继地址',
    workerPlaceholder: 'https://your-relay.workers.dev',
    relayZeroLogin: '就绪 · 免登录',
    relayConnected: (ms) => `已连通 · ${ms}ms`,
    relayOffline: '未连通',
    relayChecking: '检测中…',
    relayEnterUrl: '请输入自建地址',
    createRoom: '创建房间',
    startingQuick: '正在拉起临时通道…',
    checkingProxy: '正在检测代理…',
    inviteLabel: '邀请码',
    invitePlaceholder: '粘贴房主发来的完整邀请码',
    inviteHint: '邀请码包含加密密钥与通道地址，请完整粘贴。',
    joinRoom: '加入房间',
    joining: '正在加入…',
    roomKeyLabel: '房间码',
    copyInvite: '复制邀请码',
    copied: '已复制',
    keyHintHost: '复制内容含完整邀请码（房间码 + 加密密钥），仅发给信任的朋友。',
    waitPartnerTitle: '等待朋友加入',
    waitPartnerDesc: '请将上方复制的完整邀请码发给朋友。',
    waitApproveTitle: '等待房主批准',
    waitApproveDesc: '房主点击批准后即可连通。',
    sharingActive: '正在共享',
    sharingPaused: '共享已暂停',
    sharingDescOn: (addr, n) => `对方流量正经由 ${addr} 转发 · ${n} 个活跃连接`,
    sharingDescOff: '中继连接中断，重连成功前已自动阻断流量。',
    yourProxyExit: '你的代理出口',
    browserExit: '浏览器出口',
    verifiedSuffix: (ip) => `${ip}（已验证）`,
    expectPrefix: (ip) => `预期 ${ip}`,
    tunnelLabel: '加密隧道',
    tunNotStarted: '未启动',
    tunRelayConn: '中继已连接',
    tunReconnecting: '重连中',
    tunVerifying: '校验中…',
    tunVerified: '端到端加密 · 已验证',
    tunBlocked: '已阻断',
    tunStarting: '启动中…',
    relayRowLabel: '中继通道',
    relayDefaultVal: '默认',
    cameraLabel: '本机摄像头',
    camDetecting: '检测中…',
    camAvailable: '可用',
    camNone: '未检测到',
    syncWarn: '信令暂时中断，正在重试… 若超过 1 分钟未恢复将自动撤销共享。',
    tempChrome: '临时 Chrome 窗口',
    chromeRunning: '运行中 · 独立隔离目录 · 仅走加密隧道',
    chromeReady: '就绪 · 全新空白环境',
    chromeBlocked: '出口验证通过前保持锁定',
    launchChrome: '打开 Chrome',
    closeChrome: '关闭 Chrome',
    endSession: '结束会话',
    sessionEndedTitle: '会话已结束',
    defaultEndReason: '本次会话已结束。',
    stepSkipped: '— 无需执行',
    stepDone: '✓ 已完成',
    stepFailed: '✗ 失败',
    newSession: '开始新会话',
    modalTitle: '接入请求',
    modalBody: '有人凭你的邀请码请求借用网络。请确认是本人后再点批准。',
    modalScopeLabel: '对方获得权限',
    modalScopeVal: (addr) => `仅本地代理 ${addr}，本机其他内容不可达`,
    declineBtn: '拒绝并关房',
    approveBtn: '批准接入',
    topReady: '就绪',
    topReconn: '重连中…',
    topSigInt: '信令重连中',
    topWaiting: '等待中',
    topJoinReq: '待批准',
    topAwaitApp: '等待批准',
    topSharing: '共享中',
    topPaused: '已暂停',
    topConnHost: '已连通',
    topBlocked: '已阻断',
    topVerifying: '校验网络中',
    alertRelayFail: '自建中继无响应，请检查地址（按回车测试）或切回「临时」。',
    alertCreateFail: '无法创建房间：\n',
    alertJoinFail: '无法加入房间：\n',
    alertApproveFail: '批准失败：\n',
    alertLeaveFail: '结束会话出错：\n',
    alertBrowser: '浏览器：',
    youEnded: '你已结束本次会话。',
  },
};

function t() { return I18N[lang] || I18N.en; }

function translateServerText(str) {
  if (!str || lang !== 'zh') return str;
  const exact = {
    "Proxy exit matches this Mac's public IP. Fine if your proxy runs in TUN/global mode; otherwise check it is not in direct mode.":
      '代理出口 IP 与本机公网 IP 相同（若开启了 TUN / 全局模式属正常现象，否则请检查代理是否处于直连模式）。',
    'Verifying the host\'s network exit…': '正在校验房主的网络出口…',
    "Lost the host's network. Browser traffic is blocked until it recovers.":
      '与房主网络断开，恢复前浏览器流量已自动阻断。',
    "The host's exit IP is the same as your own public IP (same network or VPN node?).":
      '房主出口 IP 与你本机公网 IP 相同（可能在同一网络或使用了相同节点）。',
    'You ended the session.': '你已结束本次会话。',
    'The host declined or closed the room.': '房主拒绝了请求或已关闭房间。',
    'The host ended the session or the room expired.': '房主已结束共享或房间已过期。',
    'Your partner left or the room expired.': '对方已离开或房间已到期。',
    'Lost contact with the signaling server for over a minute; access was revoked.':
      '信令连接中断超过 1 分钟，已自动收回访问权限。',
    'Stop sharing your proxy (all partner connections cut)': '停止共享本地代理（已切断所有连接）',
    'Stop sharing your proxy': '停止共享本地代理',
    'Close the encrypted tunnel port': '关闭本地加密隧道端口',
    'Quit the temporary Chrome and delete its profile': '关闭临时 Chrome 并彻底擦除浏览数据',
    'Quit the temporary Chrome': '关闭临时 Chrome',
    'Close the room on the server': '在信令端销毁房间',
    'Destroy temporary trycloudflare.com tunnel': '销毁临时 trycloudflare.com 穿透通道',
    'will retry; the server also drops it within 45s of no contact': '稍后自动重试；无心跳 45 秒后也会自动销毁',
  };
  if (exact[str]) return exact[str];
  if (str.startsWith('Cannot reach the internet through the host: '))
    return '无法经房主代理访问外网：' + str.slice('Cannot reach the internet through the host: '.length);
  if (str.startsWith('Relay link down, reconnecting (partner traffic is blocked): '))
    return '中继断开重连中（期间已阻断流量）：' + str.slice('Relay link down, reconnecting (partner traffic is blocked): '.length);
  return str;
}

function toggleLang() {
  lang = lang === 'zh' ? 'en' : 'zh';
  try { localStorage.setItem('temproom_lang', lang); } catch {}
  applyI18n();
}

function applyI18n() {
  const s = t();
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en';
  const btnLang = $('btn-lang');
  if (lang === 'zh') {
    btnLang.textContent = 'EN';
    btnLang.setAttribute('lang', 'en');
    btnLang.setAttribute('aria-label', 'Switch to English');
  } else {
    btnLang.textContent = '中文';
    btnLang.setAttribute('lang', 'zh-CN');
    btnLang.setAttribute('aria-label', '切换为中文');
  }

  $('role-tab-requester').textContent = s.shareTab;
  $('role-tab-operator').textContent = s.joinTab;
  $('title-local-proxy').textContent = s.localProxy;
  $('btn-scan-proxy').textContent = s.scan;
  $('relay-group-label').textContent = s.relayTitle;
  $('label-opt-quick').textContent = s.optQuick;
  $('label-opt-public').textContent = s.optPublic;
  $('label-opt-custom').textContent = s.optCustom;
  $('input-worker-url').setAttribute('aria-label', s.workerAria);
  $('input-worker-url').placeholder = s.workerPlaceholder;

  const btnCreate = $('btn-create-room');
  if (createBusyMode === 'quick') btnCreate.textContent = s.startingQuick;
  else if (createBusyMode === 'other') btnCreate.textContent = s.checkingProxy;
  else btnCreate.textContent = s.createRoom;

  $('label-invite-code').textContent = s.inviteLabel;
  $('input-room-key').placeholder = s.invitePlaceholder;
  $('hint-invite-code').textContent = s.inviteHint;
  $('btn-join-room').textContent = joinBusy ? s.joining : s.joinRoom;

  $('label-room-key').textContent = s.roomKeyLabel;
  if ($('copy-badge').dataset.copied !== '1') $('copy-badge').textContent = s.copyInvite;
  $('metric-tunnel-label').textContent = s.tunnelLabel;
  $('metric-relay-label').textContent = s.relayRowLabel;
  $('metric-camera-label').textContent = s.cameraLabel;
  $('label-temp-chrome').textContent = s.tempChrome;
  $('btn-leave-room').textContent = s.endSession;

  $('title-session-ended').textContent = s.sessionEndedTitle;
  $('btn-new-session').textContent = s.newSession;

  $('modal-title-text').textContent = s.modalTitle;
  $('modal-body-text').textContent = s.modalBody;
  $('modal-scope-label').textContent = s.modalScopeLabel;
  $('btn-decline-join').textContent = s.declineBtn;
  $('btn-approve-join').textContent = s.approveBtn;

  renderProxyList();
  renderRelay();
  refreshRelayStatusLabel();
  updateCameraLabel();
  if (currentSession) renderSession(currentSession);
  else if ($('view-cleanup').classList.contains('active')) renderEndedView(lastEnded);
  updateTopbar();
}

function bindIMESafeEnter(inputEl, action) {
  inputEl.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (e.isComposing || e.keyCode === 229) return;
      action();
    }
  });
}

document.addEventListener('DOMContentLoaded', () => {
  applyI18n();
  refreshProxies();
  loadRelay();
  startPolling();
  bindIMESafeEnter($('input-worker-url'), saveCustomRelay);
  bindIMESafeEnter($('input-room-key'), joinRoom);
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
      $('status-text').textContent = t().topReconn;
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

function renderProxyList() {
  const list = $('proxy-list');
  const s = t();
  list.innerHTML = '';
  if (proxiesScanning) {
    const item = document.createElement('div');
    item.className = 'proxy-item';
    const meta = document.createElement('span');
    meta.className = 'proxy-meta';
    meta.textContent = s.scanningProxies;
    item.appendChild(meta);
    list.appendChild(item);
    return;
  }
  if (!lastProxies || lastProxies.length === 0) {
    const item = document.createElement('div');
    item.className = 'proxy-item selected';
    const addr = document.createElement('span');
    addr.className = 'proxy-address';
    addr.textContent = '127.0.0.1:7890';
    const meta = document.createElement('span');
    meta.className = 'proxy-meta';
    meta.textContent = s.noProxy;
    item.append(addr, meta);
    list.appendChild(item);
    return;
  }
  lastProxies.forEach((res) => {
    const item = document.createElement('div');
    item.className = 'proxy-item' + (res.address === selectedProxy ? ' selected' : '');
    const addr = document.createElement('span');
    addr.className = 'proxy-address';
    addr.textContent = res.address;
    const meta = document.createElement('span');
    meta.className = 'proxy-meta';
    meta.textContent = `${res.latencyMs}ms · ${res.exitIp ? s.exitPrefix + res.exitIp : s.noInternet}`;
    item.append(addr, meta);
    item.onclick = () => {
      selectedProxy = res.address;
      renderProxyList();
    };
    list.appendChild(item);
  });
}

async function refreshProxies() {
  proxiesScanning = true;
  renderProxyList();
  let results = [];
  try { results = await api('/proxy/probe') || []; } catch (e) { results = []; }
  proxiesScanning = false;
  lastProxies = results;
  selectedProxy = results.length > 0 ? results[0].address : '127.0.0.1:7890';
  renderProxyList();
}

// ---------- relay ----------

function setRelayState(state, text) {
  $('relay-dot').className = 'status-indicator ' + ({ ok: 'active', bad: 'failed', busy: 'waiting' }[state] || 'ready');
  $('relay-state-text').textContent = text;
}

function renderRelay() {
  show('relay-opt-public', !!relay.publicAvailable);
  $('relay-opt-quick').classList.toggle('selected', relayChoice === 'quick');
  $('relay-opt-public').classList.toggle('selected', relayChoice === 'public');
  $('relay-opt-custom').classList.toggle('selected', relayChoice === 'custom');
  $('relay-opt-quick').setAttribute('aria-checked', relayChoice === 'quick');
  $('relay-opt-public').setAttribute('aria-checked', relayChoice === 'public');
  $('relay-opt-custom').setAttribute('aria-checked', relayChoice === 'custom');
  show('input-worker-url', relayChoice === 'custom');
}

function refreshRelayStatusLabel() {
  const s = t();
  if (relayBusy) {
    setRelayState('busy', s.relayChecking);
    return;
  }
  if (relayChoice === 'quick') {
    setRelayState('ok', s.relayZeroLogin);
    $('relay-state').title = 'trycloudflare.com';
    return;
  }
  if (relayChoice === 'custom' && !$('input-worker-url').value.trim() && !relay.customWorkerUrl) {
    setRelayState('', s.relayEnterUrl);
    $('relay-state').title = '';
    return;
  }
  showHealth(lastHealth);
}

function showHealth(h) {
  lastHealth = h;
  const s = t();
  if (h && h.ok && h.url === 'trycloudflare.com') setRelayState('ok', s.relayZeroLogin);
  else if (h && h.ok) setRelayState('ok', s.relayConnected(h.latencyMs));
  else setRelayState('bad', s.relayOffline);
  $('relay-state').title = (h && h.error) || (h && h.url) || '';
}

async function loadRelay() {
  try { relay = await api('/relay'); } catch (e) { /* keep defaults */ }
  relayChoice = relay.mode || (relay.publicAvailable ? 'public' : 'quick');
  $('relay-opt-public').disabled = !relay.publicAvailable;
  $('input-worker-url').value = relay.customWorkerUrl || '';
  renderRelay();
  if (relayChoice === 'quick') {
    lastHealth = { ok: true, url: 'trycloudflare.com' };
    refreshRelayStatusLabel();
    return;
  }
  if (relayChoice === 'custom' && !relay.customWorkerUrl) {
    refreshRelayStatusLabel();
    return;
  }
  relayBusy = true;
  refreshRelayStatusLabel();
  try {
    const h = await api('/relay/check', { method: 'POST', body: '{}' });
    relayBusy = false;
    showHealth(h);
  } catch (e) {
    relayBusy = false;
    showHealth(null);
  }
}

async function applyRelay(mode, url) {
  relayBusy = true;
  refreshRelayStatusLabel();
  let res;
  try { res = await api('/relay', { method: 'POST', body: JSON.stringify({ mode, url }) }); }
  catch (e) { res = { config: relay, health: { ok: false, error: e.message }, error: e.message }; }
  relayBusy = false;
  relay = res.config || relay;
  showHealth(res.health);
  $('input-worker-url').classList.toggle('invalid', !!res.error && mode === 'custom');
  return !res.error;
}

async function chooseRelay(mode) {
  if (mode === 'public' && !relay.publicAvailable) return;
  relayChoice = mode;
  renderRelay();
  if (mode === 'quick') {
    $('input-worker-url').classList.remove('invalid');
    await applyRelay('quick', '');
  } else if (mode === 'public') {
    $('input-worker-url').classList.remove('invalid');
    await applyRelay('public', '');
  } else if ($('input-worker-url').value.trim()) {
    await saveCustomRelay();
  } else {
    refreshRelayStatusLabel();
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
  if (relayChoice === 'quick') return relay.mode === 'quick' || applyRelay('quick', '');
  if (relayChoice === 'public') return relay.mode === 'public' || applyRelay('public', '');
  const typed = $('input-worker-url').value.trim();
  if (relay.mode === 'custom' && typed === relay.customWorkerUrl) return true;
  return saveCustomRelay();
}

async function createRoom() {
  const btn = $('btn-create-room');
  await mutate(async () => {
    if (!(await relayReady())) {
      alert(t().alertRelayFail);
      return;
    }
    btn.disabled = true;
    createBusyMode = relayChoice === 'quick' ? 'quick' : 'other';
    btn.textContent = createBusyMode === 'quick' ? t().startingQuick : t().checkingProxy;
    try {
      renderSession(await api('/room/create', { method: 'POST', body: JSON.stringify({ proxy: selectedProxy }) }));
    } catch (err) {
      alert(t().alertCreateFail + err.message);
    } finally {
      createBusyMode = '';
      btn.disabled = false;
      btn.textContent = t().createRoom;
    }
  });
}

async function joinRoom() {
  const input = $('input-room-key');
  const invite = input.value.trim();
  if (!invite) { input.focus(); return; }
  const btn = $('btn-join-room');
  await mutate(async () => {
    joinBusy = true;
    btn.disabled = true;
    btn.textContent = t().joining;
    try {
      renderSession(await api('/room/join', { method: 'POST', body: JSON.stringify({ invite }) }));
    } catch (err) {
      alert(t().alertJoinFail + err.message);
    } finally {
      joinBusy = false;
      btn.disabled = false;
      btn.textContent = t().joinRoom;
    }
  });
}

// ---------- session ----------

async function approveJoin() {
  await mutate(async () => {
    try { renderSession(await api('/room/approve', { method: 'POST' })); }
    catch (err) { alert(t().alertApproveFail + err.message); }
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
      alert(t().alertLeaveFail + err.message);
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
    alert(t().alertBrowser + err.message);
  } finally {
    btn.disabled = false;
    if (currentSession) renderSession(currentSession);
  }
}

function copyInvite() {
  if (!currentSession || !currentSession.invite) return;
  navigator.clipboard.writeText(currentSession.invite).then(() => {
    const badge = $('copy-badge');
    badge.dataset.copied = '1';
    badge.textContent = t().copied;
    setTimeout(() => {
      badge.dataset.copied = '0';
      badge.textContent = t().copyInvite;
    }, 1500);
  });
}

function setNote(text, level) {
  const note = $('net-note');
  if (!text) { note.className = 'net-note hidden'; return; }
  note.className = 'net-note' + (level ? ' ' + level : '');
  note.textContent = text;
}

function show(id, visible) { $(id).classList.toggle('hidden', !visible); }

function updateCameraLabel() {
  const s = t();
  let txt = s.camDetecting;
  if (cameraInfo) {
    txt = cameraInfo.available ? (cameraInfo.name || s.camAvailable) : s.camNone;
  }
  $('metric-camera').textContent = txt;
}

function renderSession(session) {
  if (!session) return;
  currentSession = session;
  const s = t();
  showView('view-session');
  const host = session.role === 'REQUESTER';
  const st = session.state;
  const net = session.network || {};

  $('display-room-key').textContent = session.roomKey || '----';
  show('copy-badge', host);
  $('key-wrap').style.cursor = host ? 'pointer' : 'default';
  $('key-hint').textContent = host ? s.keyHintHost : '';

  // Waiting card
  const waiting = (host && st === 'WAITING') || (!host && st === 'PENDING_APPROVAL');
  show('waiting-card', waiting);
  if (host) {
    $('waiting-title').textContent = s.waitPartnerTitle;
    $('waiting-desc').textContent = s.waitPartnerDesc;
  } else {
    $('waiting-title').textContent = s.waitApproveTitle;
    $('waiting-desc').textContent = s.waitApproveDesc;
  }

  // Join request modal (host only, only while pending)
  show('approval-modal', host && st === 'PENDING_APPROVAL');
  if (host) $('modal-scope').textContent = s.modalScopeVal(net.proxyAddress || '');

  // Host sharing card
  show('peer-active-card', host && st === 'ACTIVE');
  if (host && st === 'ACTIVE') {
    $('peer-title').textContent = net.relayOnline ? s.sharingActive : s.sharingPaused;
    $('peer-dot').className = 'status-indicator ' + (net.relayOnline ? 'active' : 'waiting');
    $('peer-desc').textContent = net.relayOnline
      ? s.sharingDescOn(net.proxyAddress, net.streams || 0)
      : s.sharingDescOff;
  }

  // Metrics
  if (host) {
    $('metric-exit-label').textContent = s.yourProxyExit;
    $('metric-exit-ip').textContent = net.expectedExitIp || '—';
    setTunnel(st !== 'ACTIVE' ? [s.tunNotStarted, ''] : net.relayOnline ? [s.tunRelayConn, 'status-connected'] : [s.tunReconnecting, 'status-connecting']);
  } else {
    $('metric-exit-label').textContent = s.browserExit;
    $('metric-exit-ip').textContent = net.state === 'VERIFIED' ? s.verifiedSuffix(net.exitIp) : (net.expectedExitIp ? s.expectPrefix(net.expectedExitIp) : '—');
    const map = {
      CONNECTING: [s.tunVerifying, 'status-connecting'],
      VERIFIED: [s.tunVerified, 'status-connected'],
      FAILED: [s.tunBlocked, 'status-disconnected'],
    };
    setTunnel(st !== 'ACTIVE' ? [s.tunNotStarted, ''] : (map[net.state] || [s.tunStarting, 'status-connecting']));
  }
  updateCameraLabel();
  $('metric-relay').textContent = session.relay ? session.relay : s.relayDefaultVal;

  // Notes: errors beat warnings beat sync problems
  const msg = translateServerText(net.message);
  const warn = translateServerText(net.warning);
  if (msg && (net.state === 'FAILED' || (host && st === 'ACTIVE' && !net.relayOnline))) setNote(msg, 'error');
  else if (msg) setNote(msg, '');
  else if (warn) setNote(warn, 'warn');
  else if (session.syncError) setNote(s.syncWarn, 'warn');
  else setNote('', '');

  // Operator browser
  show('operator-tools', !host && st === 'ACTIVE');
  const btn = $('btn-chrome');
  if (session.chromeRunning) {
    $('chrome-status-text').textContent = s.chromeRunning;
    btn.textContent = s.closeChrome;
    btn.classList.replace('btn-secondary', 'btn-danger');
    btn.disabled = false;
  } else {
    const ok = net.state === 'VERIFIED';
    $('chrome-status-text').textContent = ok ? s.chromeReady : s.chromeBlocked;
    btn.textContent = s.launchChrome;
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
  const tr = t();
  if (!s) { text.textContent = tr.topReady; dot.className = 'status-indicator ready'; return; }
  const net = s.network || {};
  let label = s.state, cls = 'waiting';
  if (s.syncError) { label = tr.topSigInt; }
  else if (s.state === 'WAITING') label = tr.topWaiting;
  else if (s.state === 'PENDING_APPROVAL') label = s.role === 'REQUESTER' ? tr.topJoinReq : tr.topAwaitApp;
  else if (s.state === 'ACTIVE' && s.role === 'REQUESTER') {
    label = net.relayOnline ? tr.topSharing : tr.topPaused;
    cls = net.relayOnline ? 'active' : 'waiting';
  } else if (s.state === 'ACTIVE') {
    if (net.state === 'VERIFIED') { label = tr.topConnHost; cls = 'active'; }
    else if (net.state === 'FAILED') { label = tr.topBlocked; cls = 'failed'; }
    else label = tr.topVerifying;
  }
  text.textContent = label;
  dot.className = 'status-indicator ' + cls;
}

function updateStatus(status) {
  if (!status) return;
  cameraInfo = status.camera || { available: false };
  updateCameraLabel();
  if (!currentSession) updateTopbar();
}

// ---------- ended ----------

function showEnded(ended) {
  currentSession = null;
  lastEnded = ended;
  $('approval-modal').classList.add('hidden');
  setNote('', '');
  showView('view-cleanup');
  renderEndedView(ended);
  updateTopbar();
}

function renderEndedView(ended) {
  const s = t();
  $('end-reason').textContent = translateServerText(ended && ended.reason) || s.defaultEndReason;
  const container = $('cleanup-steps');
  container.innerHTML = '';
  ((ended && ended.results) || []).forEach(res => {
    const row = document.createElement('div');
    row.className = 'step-item';
    const name = document.createElement('span');
    name.className = 'step-name';
    const stepText = translateServerText(res.step);
    const errText = translateServerText(res.error);
    name.textContent = stepText + (errText ? ` — ${errText}` : '');
    const st = document.createElement('span');
    st.className = 'step-status ' + (res.skipped ? 'skipped' : res.success ? '' : 'failed');
    st.textContent = res.skipped ? s.stepSkipped : res.success ? s.stepDone : s.stepFailed;
    row.append(name, st);
    container.appendChild(row);
  });
}

function resetToIdle() {
  currentSession = null;
  lastEnded = null;
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
