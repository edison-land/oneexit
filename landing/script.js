/**
 * TempRoom Landing Page — Interactive Visualizer State Controller
 * Adhering to Linear / Apple minimal UI principles:
 * "能用图示就不要用文字" (If you can use diagrams/visuals, don't use words).
 */

(function () {
  'use strict';

  // DOM Elements
  const stageNavButtons = document.querySelectorAll('.stage-step');
  const nodeA = document.getElementById('nodeA');
  const nodeB = document.getElementById('nodeB');
  const badgeA = document.getElementById('badgeA');
  const badgeB = document.getElementById('badgeB');
  const displayKey = document.getElementById('displayKey');
  const modalA = document.getElementById('modalA');
  const keyBoxB = document.getElementById('inputKeyB');
  const chromeStatusB = document.getElementById('chromeStatusB');
  const exitStatusB = document.getElementById('exitStatusB');
  const chromePreview = document.getElementById('chromePreview');
  const shredderOverlay = document.getElementById('shredderOverlay');
  const beamGlow = document.getElementById('beamGlow');
  const particles = [
    document.getElementById('p1'),
    document.getElementById('p2'),
    document.getElementById('p3'),
  ];
  const tunnelStatusText = document.getElementById('tunnelStatusText');
  const circuitBreaker = document.getElementById('circuitBreaker');
  const keyBoxA = document.getElementById('keyBoxA');

  let currentStep = 1;
  let autoTimer = null;
  let userInteracted = false;
  const STEP_DURATION = 4200; // 4.2s per step in auto loop

  const ROOM_KEY_SAMPLE = 'PJWA-RY2P';

  /**
   * Set visualizer state to step 1..4
   */
  function setStep(stepNum) {
    currentStep = stepNum;

    // 1. Update Navigation Buttons
    stageNavButtons.forEach((btn) => {
      const step = parseInt(btn.getAttribute('data-step'), 10);
      btn.classList.toggle('active', step === stepNum);
    });

    // 2. Clear Active State Highlights
    nodeA.classList.remove('active-highlight');
    nodeB.classList.remove('active-highlight');

    // 3. Render Step-Specific UI States
    switch (stepNum) {
      case 1: // Generate Room
        nodeA.classList.add('active-highlight');
        badgeA.textContent = 'READY';
        badgeA.className = 'mini-badge';
        displayKey.textContent = ROOM_KEY_SAMPLE;
        modalA.classList.add('hidden');

        // Relay
        beamGlow.classList.remove('active');
        particles.forEach((p) => p.classList.remove('active'));
        tunnelStatusText.textContent = 'Standby / Waiting Room Key';

        // Machine B
        badgeB.textContent = 'IDLE';
        badgeB.className = 'mini-badge';
        keyBoxB.textContent = '--------';
        keyBoxB.style.color = 'var(--text-muted)';
        chromeStatusB.textContent = 'Ready (Ephemeral)';
        exitStatusB.textContent = 'Direct ISP';
        chromePreview.classList.add('hidden');
        shredderOverlay.classList.add('hidden');
        break;

      case 2: // Authorize Peer
        nodeA.classList.add('active-highlight');
        badgeA.textContent = 'PENDING';
        badgeA.className = 'mini-badge';
        modalA.classList.remove('hidden');

        // Relay
        beamGlow.classList.remove('active');
        particles.forEach((p) => p.classList.remove('active'));
        tunnelStatusText.textContent = 'Handshake in Progress (Cloud Relay)';

        // Machine B
        badgeB.textContent = 'CONNECTING...';
        badgeB.className = 'mini-badge';
        keyBoxB.textContent = ROOM_KEY_SAMPLE;
        keyBoxB.style.color = 'var(--accent-indigo)';
        chromeStatusB.textContent = 'Mounting /tmp ramdisk...';
        exitStatusB.textContent = 'Awaiting A Auth';
        chromePreview.classList.add('hidden');
        shredderOverlay.classList.add('hidden');
        break;

      case 3: // Active Egress
        nodeB.classList.add('active-highlight');
        badgeA.textContent = 'CONNECTED';
        badgeA.className = 'mini-badge active';
        modalA.classList.add('hidden');

        // Relay
        beamGlow.classList.add('active');
        particles.forEach((p) => p.classList.add('active'));
        tunnelStatusText.textContent = 'Encrypted Tunnel Active (Cloud Edge)';

        // Machine B
        badgeB.textContent = 'TUNNELED';
        badgeB.className = 'mini-badge active';
        chromeStatusB.textContent = 'Running: PID 48102';
        exitStatusB.textContent = '203.0.113.7 (A)';
        exitStatusB.classList.add('text-accent');
        chromePreview.classList.remove('hidden');
        shredderOverlay.classList.add('hidden');
        break;

      case 4: // Instant Shred
        badgeA.textContent = 'CLOSING';
        badgeA.className = 'mini-badge';
        modalA.classList.add('hidden');

        // Relay
        beamGlow.classList.remove('active');
        particles.forEach((p) => p.classList.remove('active'));
        tunnelStatusText.textContent = 'Session Severed & Purged';

        // Machine B
        badgeB.textContent = 'SHREDDED';
        badgeB.className = 'mini-badge';
        chromeStatusB.textContent = 'Zero Remnants';
        exitStatusB.textContent = 'Direct ISP';
        exitStatusB.classList.remove('text-accent');
        chromePreview.classList.add('hidden');
        shredderOverlay.classList.remove('hidden');
        break;

      default:
        break;
    }
  }

  /**
   * Auto-loop sequencer
   */
  function startAutoLoop() {
    stopAutoLoop();
    autoTimer = setInterval(() => {
      let nextStep = currentStep + 1;
      if (nextStep > 4) nextStep = 1;
      setStep(nextStep);
    }, STEP_DURATION);
  }

  function stopAutoLoop() {
    if (autoTimer) {
      clearInterval(autoTimer);
      autoTimer = null;
    }
  }

  /**
   * Step button click events
   */
  stageNavButtons.forEach((btn) => {
    btn.addEventListener('click', () => {
      const step = parseInt(btn.getAttribute('data-step'), 10);
      setStep(step);

      // Pause auto-loop on manual interaction for 10s then resume
      stopAutoLoop();
      clearTimeout(window._resumeTimeout);
      window._resumeTimeout = setTimeout(() => {
        startAutoLoop();
      }, 10000);
    });
  });

  /**
   * Interactive Modal Allow / Deny buttons in Step 2
   */
  const btnAllow = document.querySelector('.btn-micro-allow');
  const btnDeny = document.querySelector('.btn-micro-deny');

  if (btnAllow) {
    btnAllow.addEventListener('click', () => {
      setStep(3);
    });
  }

  if (btnDeny) {
    btnDeny.addEventListener('click', () => {
      setStep(1);
    });
  }

  /**
   * Interactive Copy Key Simulator
   */
  if (keyBoxA) {
    keyBoxA.style.cursor = 'pointer';
    keyBoxA.setAttribute('title', 'Click to simulate copy');
    keyBoxA.addEventListener('click', () => {
      const origText = displayKey.textContent;
      if (navigator.clipboard) {
        navigator.clipboard.writeText(origText).catch(() => {});
      }
      displayKey.textContent = 'COPIED!';
      setTimeout(() => {
        displayKey.textContent = origText;
      }, 1200);
    });
  }

  /**
   * Circuit breaker interactive toggle in Bento Card
   */
  if (circuitBreaker) {
    circuitBreaker.style.cursor = 'pointer';
    circuitBreaker.addEventListener('click', () => {
      circuitBreaker.classList.toggle('severed');
    });
  }

  // Initial step setup & kick off loop
  setStep(1);
  startAutoLoop();
})();
