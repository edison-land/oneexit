(function () {
  'use strict';

  const STEP_MS = 1200; // 1.2s per step
  const isZh = (document.documentElement.lang || '').toLowerCase().startsWith('zh');
  const metaScheme = document.querySelector('meta[name="color-scheme"]');
  const themeToggle = document.getElementById('themeToggle');
  const stageFrame = document.querySelector('.stage-frame');
  const stepBtns = document.querySelectorAll('.step');
  const playToggle = document.getElementById('playToggle');
  const nodeA = document.getElementById('nodeA');
  const nodeB = document.getElementById('nodeB');
  const badgeA = document.getElementById('badgeA');
  const badgeB = document.getElementById('badgeB');
  const displayKey = document.getElementById('displayKey');
  const keyBoxA = document.getElementById('keyBoxA');
  const modalA = document.getElementById('modalA');
  const beamGlow = document.getElementById('beamGlow');
  const particles = [
    document.getElementById('p1'),
    document.getElementById('p2'),
    document.getElementById('p3'),
  ];
  const tunnelStatusText = document.getElementById('tunnelStatusText');
  const chromeSlot = document.getElementById('chromeSlot');
  const chromeStatusB = document.getElementById('chromeStatusB');
  const chromeBar = document.getElementById('chromeBar');
  const chromeReadout = document.getElementById('chromeReadout');

  const SAMPLE_CODE = 'PJWA-RY2P';

  const STR = isZh
    ? {
        play: '播放动画',
        pause: '暂停动画',
        copiedKey: '已复制 ✓',
        copyOk: '已复制',
        copyFail: '复制失败',
        steps: {
          1: {
            a: '就绪',
            bridge: '免登录自动拉起',
            b: '等待中',
            cState: '已锁定',
            cBar: 'chatgpt.com · stripe.com',
            cOut: '粘贴房间码',
          },
          2: {
            a: '待批准',
            bridge: '正在握手…',
            b: '接入中',
            cState: '待审批',
            cBar: 'chatgpt.com · stripe.com',
            cOut: '等待你批准',
          },
          3: {
            a: '共享中',
            bridge: '全程加密 · 私密',
            b: '已连通',
            cState: '已打开',
            cBar: 'chatgpt.com · stripe.com',
            cOut: '已走你的 IP ✓',
          },
          4: {
            a: '已停止',
            bridge: '已断开',
            b: '已清理',
            cState: '已关闭',
            cBar: 'Cookie 与浏览记录',
            cOut: '已彻底清除 ✓',
          },
        },
      }
    : {
        play: 'Play',
        pause: 'Pause',
        copiedKey: 'Copied ✓',
        copyOk: 'Copied',
        copyFail: 'Copy failed',
        steps: {
          1: {
            a: 'Ready',
            bridge: 'Auto · No Login',
            b: 'Waiting',
            cState: 'Locked',
            cBar: 'chatgpt.com · stripe.com',
            cOut: 'Paste code',
          },
          2: {
            a: '1 Request',
            bridge: 'Connecting…',
            b: 'Joining',
            cState: 'Pending',
            cBar: 'chatgpt.com · stripe.com',
            cOut: 'Wait approval',
          },
          3: {
            a: 'Sharing',
            bridge: 'Encrypted · Private',
            b: 'Connected',
            cState: 'Open',
            cBar: 'chatgpt.com · stripe.com',
            cOut: 'Using Your IP ✓',
          },
          4: {
            a: 'Stopped',
            bridge: 'Closed',
            b: 'Cleaned',
            cState: 'Closed',
            cBar: 'Cookies & history',
            cOut: 'Deleted ✓',
          },
        },
      };

  // ---- Light / Dark Theme Toggle ----
  function systemScheme() {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  function renderedScheme() {
    const val = metaScheme ? metaScheme.content : 'light dark';
    if (val === 'dark' || val === 'light') return val;
    return systemScheme();
  }

  if (themeToggle && metaScheme) {
    themeToggle.addEventListener('click', () => {
      const target = renderedScheme() === 'dark' ? 'light' : 'dark';
      const sys = systemScheme();
      if (target !== sys) {
        try { localStorage.setItem('color-scheme', target); } catch {}
        metaScheme.content = target;
      } else {
        try { localStorage.removeItem('color-scheme'); } catch {}
        metaScheme.content = 'light dark';
      }
    });

    window.addEventListener('storage', (e) => {
      if (e.key === 'color-scheme') {
        metaScheme.content = e.newValue ?? 'light dark';
      }
    });
  }

  // ---- 1.2s Kinetic Story Loop ----
  let currentStep = 1;
  let timer = null;
  let resumeTimer = null;
  let paused = false;

  function setStep(step) {
    currentStep = step;
    const s = STR.steps[step];
    if (!s) return;

    stepBtns.forEach((btn) => {
      const isAct = parseInt(btn.dataset.step, 10) === step;
      btn.classList.remove('active');
      if (isAct) {
        void btn.offsetWidth;
        btn.classList.add('active');
      }
      btn.setAttribute('aria-pressed', String(isAct));
    });

    nodeA.classList.toggle('active', step === 1 || step === 2);
    nodeB.classList.toggle('active', step === 3 || step === 4);
    modalA.classList.toggle('visible', step === 2);
    modalA.setAttribute('aria-hidden', String(step !== 2));

    beamGlow.classList.toggle('active', step === 3);
    particles.forEach((p) => p.classList.toggle('active', step === 3));

    chromeSlot.classList.remove('live', 'wiped');
    if (step === 3) chromeSlot.classList.add('live');
    if (step === 4) {
      void chromeSlot.offsetWidth;
      chromeSlot.classList.add('wiped');
    }

    const tone = step === 2 ? 'wait' : step === 3 ? 'ok' : step === 4 ? 'cut' : '';
    const outTone = step === 2 ? 'warn' : step === 3 ? 'ok' : step === 4 ? 'bad' : '';

    badgeA.textContent = s.a;
    badgeA.className = 'state-pill' + (tone ? ' ' + tone : '');
    tunnelStatusText.textContent = s.bridge;
    badgeB.textContent = s.b;
    badgeB.className = 'state-pill' + (tone ? ' ' + tone : '');
    chromeStatusB.textContent = s.cState;
    chromeStatusB.className = 'mono slot-state' + (outTone ? ' ' + outTone : '');
    chromeBar.textContent = s.cBar;
    chromeReadout.textContent = s.cOut;
    chromeReadout.className = 'exit-readout' + (outTone ? ' ' + outTone : '');
  }

  function stopLoop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  function startLoop() {
    stopLoop();
    if (paused) return;
    timer = setInterval(() => {
      setStep(currentStep >= 4 ? 1 : currentStep + 1);
    }, STEP_MS);
  }

  function setPaused(nextPaused) {
    paused = nextPaused;
    clearTimeout(resumeTimer);
    if (stageFrame) stageFrame.classList.toggle('paused', paused);
    if (playToggle) playToggle.setAttribute('aria-label', paused ? STR.play : STR.pause);
    if (paused) stopLoop();
    else {
      setStep(currentStep);
      startLoop();
    }
  }

  stepBtns.forEach((btn) => {
    btn.addEventListener('click', () => {
      const target = parseInt(btn.dataset.step, 10);
      stopLoop();
      clearTimeout(resumeTimer);
      if (stageFrame) stageFrame.classList.add('paused');
      setStep(target);
      if (!paused) {
        resumeTimer = setTimeout(() => {
          if (stageFrame) stageFrame.classList.remove('paused');
          setStep(currentStep);
          startLoop();
        }, 3600);
      }
    });
  });

  if (playToggle) {
    playToggle.addEventListener('click', () => {
      setPaused(!paused);
    });
  }

  const approveBtn = document.getElementById('approveBtn');
  const declineBtn = document.querySelector('.s-decline');
  if (approveBtn) {
    approveBtn.addEventListener('click', () => {
      setStep(3);
    });
  }
  if (declineBtn) {
    declineBtn.addEventListener('click', () => {
      setStep(1);
    });
  }

  if (keyBoxA) {
    keyBoxA.addEventListener('click', () => {
      if (navigator.clipboard) {
        navigator.clipboard.writeText(SAMPLE_CODE).catch(() => {});
      }
      displayKey.textContent = STR.copiedKey;
      setTimeout(() => {
        displayKey.textContent = SAMPLE_CODE;
      }, 1000);
    });
  }

  // ---- Accessible Copy Button (Invoker Commands + Fallback) ----
  const copyTimers = new WeakMap();
  let lastAnnouncement = '';

  function legacyCopy(text) {
    const field = Object.assign(document.createElement('textarea'), { value: text });
    field.setAttribute('readonly', '');
    field.style.cssText = 'position:fixed;inset-block-start:0;opacity:0';
    document.body.append(field);
    field.select();
    try {
      return document.execCommand('copy');
    } finally {
      field.remove();
    }
  }

  function reportCopy(button, ok) {
    const tip = button.nextElementSibling;
    clearTimeout(copyTimers.get(button));
    button.removeAttribute('data-copied');
    button.removeAttribute('data-failed');
    let msg = ok ? STR.copyOk : STR.copyFail;
    if (msg === lastAnnouncement) msg += '\u00A0';
    lastAnnouncement = msg;
    if (tip) tip.textContent = '';
    void button.offsetWidth;
    button.setAttribute(ok ? 'data-copied' : 'data-failed', '');
    if (tip) {
      requestAnimationFrame(() => {
        tip.textContent = msg;
      });
    }
    copyTimers.set(
      button,
      setTimeout(() => {
        button.removeAttribute('data-copied');
        button.removeAttribute('data-failed');
        if (tip) tip.textContent = '';
      }, 1800)
    );
  }

  async function runCopy(button, target) {
    const text = target.matches('input, textarea') ? target.value : target.innerText.trim();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      reportCopy(button, true);
    } catch {
      reportCopy(button, legacyCopy(text));
    }
  }

  if ('CommandEvent' in window) {
    document.addEventListener(
      'command',
      (event) => {
        if (event.command && event.command.startsWith('--copy')) {
          runCopy(event.source, event.target);
        }
      },
      true
    );
  } else {
    document.addEventListener('click', (event) => {
      const button = event.target.closest('button[command^="--copy"]');
      if (!button) return;
      const target = document.getElementById(button.getAttribute('commandfor'));
      if (target) runCopy(button, target);
    });
  }

  setStep(1);
  startLoop();
})();
