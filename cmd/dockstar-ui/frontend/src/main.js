import { createIcons, icons } from 'lucide';
import './styles.css';

const root = document.getElementById('app');
const views = [
  ['overview', 'house', 'Overview'],
  ['displays', 'monitor', 'Displays'],
  ['automation', 'zap', 'Automation'],
  ['wake', 'power', 'Wake & power'],
  ['diagnostics', 'activity', 'Diagnostics'],
];
const layoutOptions = [
  ['extend', 'panels-top-left', 'Extend'],
  ['mirror', 'copy', 'Mirror'],
  ['external-only', 'monitor-up', 'External only'],
  ['internal-only', 'laptop', 'Internal only'],
];

let state = null;
let draft = null;
let activeView = views.some(([id]) => id === window.location.hash.slice(1)) ? window.location.hash.slice(1) : 'overview';
let selectedDeviceID = '';
let diagnostics = '';
let dirty = false;
let busy = false;
let toastTimer = 0;
let toastMessage = '';
let toastError = false;
let renderedView = '';
const viewScrollPositions = new Map();

init();

async function init() {
  root.innerHTML = renderBoot();
  try {
    await waitForBackend();
    state = await callBackend('GetState');
  } catch (error) {
    if (isDevelopmentPreview()) {
      state = demoState();
    } else {
      renderFatal(error);
      return;
    }
  }
  resetDraft();
  render();
  window.setInterval(refreshQuietly, 5000);
}

function backend() {
  return window.go?.main?.App;
}

async function waitForBackend() {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (backend()) return;
    await new Promise((resolve) => window.setTimeout(resolve, 50));
  }
  throw new Error('Dockstar backend bindings are unavailable.');
}

function isDevelopmentPreview() {
  return ['127.0.0.1', 'localhost'].includes(window.location.hostname);
}

async function callBackend(method, ...args) {
  const target = backend();
  if (target?.[method]) return target[method](...args);
  if (!isDevelopmentPreview()) throw new Error('Dockstar backend is unavailable.');
  await new Promise((resolve) => window.setTimeout(resolve, 180));
  return mockCall(method, args);
}

function mockCall(method, args) {
  if (method === 'GetState') return structuredClone(state || demoState());
  if (method === 'GetDiagnostics') return JSON.stringify(demoDiagnostics(), null, 2);
  if (method === 'SaveConfig') {
    state.config = structuredClone(args[0]);
    state.revision += 1;
    state.config.revision = state.revision;
    refreshMockBootDiff();
    return structuredClone(state);
  }
  if (method === 'SetLayout') {
    state.config.defaultLayout = args[0];
    state.status.layout = args[0];
    state.status.docked = true;
    state.revision += 1;
    state.config.revision = state.revision;
    return structuredClone(state);
  }
  if (method === 'ApproveDisplay') {
    const id = args[0];
    const approved = args[1];
    const output = state.status.outputs.find((item) => item.deviceId === id);
    if (output) output.approved = approved;
    let rule = state.config.displays.find((item) => item.id === id);
    if (!rule) {
      rule = { id, label: output?.label || id, allowed: approved, primaryRank: 0, scale: 1, mode: 'preferred', position: 'right' };
      state.config.displays.push(rule);
    }
    rule.allowed = approved;
    state.revision += 1;
    state.config.revision = state.revision;
    refreshMockBootDiff();
    return structuredClone(state);
  }
  if (method === 'BootAction') {
    const action = args[0];
    const desired = mockApprovedEDIDs();
    const enabled = state.config.detection !== 'off' && state.config.power.keepAwakeWhileDocked && desired.length > 0;
    if (action === 'enable' && !enabled) throw new Error('Pre-login protection requires at least one explicitly approved EDID display.');
    state.boot.installed = true;
    state.boot.enabled = action === 'disable' ? false : enabled;
    state.boot.detection = state.boot.enabled ? 'allowlist' : 'off';
    state.boot.keepAwakeWhileDocked = state.boot.enabled;
    state.boot.allowedEdids = state.boot.enabled ? desired : [];
    refreshMockBootDiff();
    return structuredClone(state);
  }
  if (method === 'WakeAction') {
    state.wake.installed = args[0] !== 'restore';
    state.wake.enabled = args[0] === 'enable';
    state.wake.sources.forEach((source) => {
      source.state = state.wake.enabled ? 'enabled' : 'disabled';
    });
    return structuredClone(state);
  }
  if (method === 'SetStartupEnabled') {
    state.startup.enabled = args[0];
    state.startup.serviceEnabled = args[0];
    state.startup.trayEnabled = args[0];
    return structuredClone(state);
  }
  if (method === 'SuspendNow') return true;
  return structuredClone(state);
}

function resetDraft() {
  draft = structuredClone(state.config);
  dirty = false;
  const devices = approvalDevices();
  if (!devices.some((device) => device.deviceId === selectedDeviceID)) {
    selectedDeviceID = devices.find((device) => !device.panel && device.connected)?.deviceId
      || devices.find((device) => !device.panel)?.deviceId
      || devices[0]?.deviceId
      || '';
  }
}

function render() {
  const previousContent = root.querySelector('.content');
  if (previousContent && renderedView) {
    viewScrollPositions.set(renderedView, previousContent.scrollTop);
  }
  root.innerHTML = `
    <div class="app-shell">
      ${renderHeader()}
      ${renderSidebar()}
      <main class="content" id="main-content">
        ${renderView()}
      </main>
    </div>
    <div class="toast ${toastMessage ? 'visible' : ''} ${toastError ? 'error' : ''}" role="status" aria-live="polite">${escapeHTML(toastMessage)}</div>
  `;
  bindEvents();
  createIcons({ icons, attrs: { 'aria-hidden': 'true' } });
  root.querySelector('.content')?.scrollTo({ top: viewScrollPositions.get(activeView) || 0 });
  renderedView = activeView;
}

function renderHeader() {
  const healthy = !state.status.lastError;
  return `
    <header class="app-header">
      <div class="brand">
        <img src="/dockstar-icon.png" alt="" class="brand-icon" />
        <span>Dockstar</span>
      </div>
      <div class="header-state ${healthy ? 'healthy' : 'warning'}">
        <i data-lucide="${healthy ? 'circle-check' : 'circle-alert'}"></i>
        <span>${healthy ? (state.status.docked ? 'Docked' : 'Ready') : 'Needs attention'}</span>
      </div>
      <button class="icon-button" data-action="refresh" title="Refresh status" aria-label="Refresh status">
        <i data-lucide="refresh-cw"></i>
      </button>
    </header>
  `;
}

function renderSidebar() {
  return `
    <nav class="sidebar" aria-label="Dockstar sections">
      <div class="nav-list">
        ${views.map(([id, icon, label]) => `
          <button class="nav-item ${activeView === id ? 'selected' : ''}" data-view="${id}" aria-label="${label}" title="${label}" ${activeView === id ? 'aria-current="page"' : ''}>
            <i data-lucide="${icon}"></i><span>${label}</span>
          </button>
        `).join('')}
      </div>
      <div class="sidebar-footer">
        <span class="daemon-dot ${state.status.lastError ? 'error' : ''}"></span>
        <span>Daemon ${state.status.lastError ? 'error' : 'connected'}</span>
      </div>
    </nav>
  `;
}

function renderView() {
  switch (activeView) {
    case 'displays': return renderDisplays();
    case 'automation': return renderAutomation();
    case 'wake': return renderWake();
    case 'diagnostics': return renderDiagnostics();
    default: return renderOverview();
  }
}

function renderOverview() {
  const layout = state.status.layout || draft.defaultLayout;
  const external = connectedOutputs().find((output) => !output.panel);
  const protection = sleepProtectionSummary();
  const hardwareWakePolicy = state.wakeSupport?.manageable && state.wake.installed && state.wake.enabled;
  const hardwareWake = hardwareWakePolicy && state.wake.sources.every((source) => source.state === 'enabled');
  const hardwareWakeLabel = !state.wakeSupport?.manageable ? 'Setup required' : hardwareWake ? 'Enabled' : hardwareWakePolicy ? 'Needs apply' : 'Disabled';
  return `
    <div class="page-heading">
      <div><span class="eyebrow">Current workspace</span><h1>Overview</h1></div>
      <button class="secondary-button" data-action="refresh"><i data-lucide="refresh-cw"></i><span>Refresh</span></button>
    </div>
    <div class="overview-grid">
      <section class="surface topology-surface">
        <div class="section-heading"><h2>Display arrangement</h2><span class="status-badge ${state.status.docked ? 'good' : ''}">${state.status.docked ? 'Docked' : 'Undocked'}</span></div>
        ${renderTopology()}
        <div class="topology-summary">
          <i data-lucide="${state.status.docked ? 'circle-check' : 'unplug'}"></i>
          <strong>${state.status.docked ? layoutLabel(layout) : 'No approved dock detected'}</strong>
          <span>${external ? escapeHTML(external.label) : 'Waiting for an external display'}</span>
        </div>
      </section>
      <aside class="surface status-inspector">
        <div class="section-heading"><h2>System status</h2></div>
        ${statusRow('server', 'Daemon', state.status.lastError ? 'Error' : 'Running', !state.status.lastError)}
        ${statusRow('monitor-cog', 'Display backend', state.status.backend || 'Unavailable', Boolean(state.status.backend))}
        ${statusRow('shield-check', 'Sleep protection', protection.label, protection.good)}
        ${statusRow('power', 'Hardware wake', hardwareWakeLabel, hardwareWake)}
        ${statusRow('laptop', 'Lid', state.status.lidClosed ? 'Closed' : 'Open', true)}
        ${statusRow('clock-3', 'Last updated', relativeTime(state.status.updated), true)}
        ${state.status.lastError ? `<div class="error-message"><i data-lucide="circle-alert"></i><span>${escapeHTML(state.status.lastError)}</span></div>` : ''}
      </aside>
    </div>
    <section class="surface layout-surface">
      <div class="section-heading"><div><span class="eyebrow">Quick action</span><h2>Layout</h2></div></div>
      <div class="layout-actions">
        <div class="layout-options" role="radiogroup" aria-label="Display layout">
          ${layoutOptions.map(([value, icon, label]) => `
            <button class="layout-option ${draft.defaultLayout === value ? 'selected' : ''}" data-layout="${value}" role="radio" aria-checked="${draft.defaultLayout === value}">
              <i data-lucide="${icon}"></i><span>${label}</span>
            </button>
          `).join('')}
        </div>
        <button class="primary-button" data-action="apply-layout" ${busy ? 'disabled' : ''}><i data-lucide="check"></i><span>Apply</span></button>
      </div>
    </section>
  `;
}

function renderDisplays() {
  const devices = approvalDevices();
  const selected = devices.find((device) => device.deviceId === selectedDeviceID) || devices[0];
  return `
    <div class="page-heading">
      <div><span class="eyebrow">Hardware</span><h1>Displays</h1></div>
      <button class="primary-button" data-action="save" ${!dirty || busy ? 'disabled' : ''}><i data-lucide="save"></i><span>Save changes</span></button>
    </div>
    <section class="surface compact-topology">
      <div class="section-heading"><h2>Display arrangement</h2></div>
      ${renderTopology(selected?.deviceId)}
    </section>
    ${renderBootApprovalPanel(devices)}
    <div class="display-workspace">
      <section class="surface device-list-surface">
        <div class="section-heading"><div><span class="eyebrow">Session and pre-login</span><h2>Known displays</h2></div><span class="count-badge">${devices.length}</span></div>
        <div class="device-list">
          ${devices.length ? devices.map(renderDeviceRow).join('') : renderEmpty('monitor-off', 'No saved or detected displays')}
        </div>
      </section>
      <section class="surface display-editor">
        ${selected ? renderDisplayEditor(selected) : renderEmpty('mouse-pointer-2', 'Select a display')}
      </section>
    </div>
  `;
}

function renderBootApprovalPanel(devices) {
  const boot = state.boot || { allowedEdids: [], pendingAdditions: [], pendingRemovals: [], inSync: true };
  const support = state.bootSupport || { manageable: false, missing: [] };
  const divergence = Boolean(boot.policyMismatch || boot.pendingAdditions?.length || boot.pendingRemovals?.length);
  const label = state.bootError
    ? 'Unavailable'
    : !support.manageable
      ? 'Setup required'
      : divergence
        ? 'Changes pending'
        : boot.enabled
          ? 'Enabled'
          : boot.installed
            ? 'Disabled'
            : 'Not configured';
  const approvedCount = devices.filter((device) => device.sessionApproved && device.bootEligible).length;
  const enableReady = draft.detection !== 'off'
    && draft.power.keepAwakeWhileDocked
    && draft.displays.some((rule) => rule.allowed && isEDIDIdentity(rule.id));
  const pending = [
    ...(boot.pendingAdditions || []).map((id) => `add ${deviceName(id, devices)}`),
    ...(boot.pendingRemovals || []).map((id) => `remove ${deviceName(id, devices)}`),
  ];
  const mismatch = boot.policyMismatch
    ? approvedCount
      ? 'The pre-login policy does not match the saved session policy.'
      : 'The saved policy no longer permits pre-login protection; sync will disable it.'
    : '';
  return `
    <section class="surface boot-approval-surface">
      <div class="section-heading">
        <div><span class="eyebrow">Before sign-in</span><h2><i data-lucide="shield"></i>Pre-login sleep protection</h2></div>
        <span class="status-badge ${boot.enabled && !divergence && !state.bootError ? 'good' : ''}">${label}</span>
      </div>
      <div class="boot-approval-copy">
        <div>
          <strong>Protect only explicitly approved EDID displays before login</strong>
          <p>${enableReady ? 'This is separate from login-session startup and hardware wake. Administrator authentication is required to change the root-owned policy.' : 'Enable requires detection, session sleep protection, and at least one explicitly approved EDID display. This remains separate from hardware wake.'}</p>
        </div>
        <div class="button-group">
          <button class="secondary-button" data-boot="enable" ${busy || !support.manageable || boot.enabled || !enableReady ? 'disabled' : ''}><i data-lucide="shield-check"></i><span>Enable</span></button>
          <button class="primary-button" data-boot="sync" ${busy || !support.manageable || (!divergence && !dirty && !state.bootError) ? 'disabled' : ''}><i data-lucide="refresh-cw"></i><span>Sync saved approvals</span></button>
          <button class="quiet-button" data-boot="disable" ${busy || !support.manageable || (!state.bootError && (!boot.installed || !boot.enabled)) ? 'disabled' : ''}><i data-lucide="shield-off"></i><span>Disable</span></button>
        </div>
      </div>
      ${state.bootError ? `<div class="error-message"><i data-lucide="circle-alert"></i><span>Cannot read the installed pre-login policy: ${escapeHTML(state.bootError)}</span></div>` : ''}
      ${!support.manageable ? `<div class="error-message"><i data-lucide="package-x"></i><span>Packaged pre-login support is incomplete. Install the Dockstar system package. Missing: ${escapeHTML((support.missing || []).join(', ') || 'startup helper, policy, or unit')}.</span></div>` : ''}
      ${divergence ? `<div class="banner warning boot-divergence"><i data-lucide="triangle-alert"></i><span><strong>Pre-login policy is not synchronized.</strong>${mismatch ? ` ${escapeHTML(mismatch)}` : ''}${pending.length ? ` Pending: ${escapeHTML(pending.join('; '))}.` : ''} Session approval changes do not elevate automatically.</span></div>` : ''}
    </section>
  `;
}

function renderDeviceRow(device) {
  const mode = device.output ? currentMode(device.output) : null;
  const detail = device.connected
    ? `${device.connector || 'Connected'} · ${formatMode(mode)}`
    : device.bootEnrolled && !device.savedRule
      ? 'Disconnected · retained only by pre-login policy'
      : 'Disconnected · saved session device';
  const sessionLabel = device.panel ? 'Built-in' : device.sessionApproved ? 'Session approved' : device.savedRule ? 'Session revoked' : 'Not session-approved';
  const bootLabel = device.panel || !device.bootEligible ? 'Pre-login ineligible' : device.bootEnrolled ? 'Pre-login enrolled' : 'Not pre-login enrolled';
  return `
    <button class="device-row ${selectedDeviceID === device.deviceId ? 'selected' : ''}" data-device="${escapeAttr(device.deviceId)}">
      <span class="device-number">${device.panel ? '<i data-lucide="laptop"></i>' : '<i data-lucide="monitor"></i>'}</span>
      <span class="device-copy"><strong>${escapeHTML(device.label)}</strong><small>${escapeHTML(detail)}</small></span>
      <span class="approval-stack">
        <span class="approval-state ${device.sessionApproved ? 'approved' : ''}"><i data-lucide="${device.sessionApproved ? 'shield-check' : 'shield-alert'}"></i>${sessionLabel}</span>
        <span class="approval-state prelogin ${device.bootEnrolled ? 'approved' : ''}"><i data-lucide="${device.bootEnrolled ? 'lock-keyhole' : 'lock-open'}"></i>${bootLabel}</span>
      </span>
      <i data-lucide="chevron-right" class="row-chevron"></i>
    </button>
  `;
}

function renderDisplayEditor(device) {
  const output = device.output;
  const rule = draft.displays.find((item) => item.id === device.deviceId) || (output ? displayRule(output) : {
    id: device.deviceId, label: device.label, allowed: false, primaryRank: 0, scale: 1, mode: 'preferred', position: 'right',
  });
  const modes = uniqueModes(output?.modes || []);
  return `
    <div class="section-heading"><div><span class="eyebrow">${device.connected ? 'Detected display' : 'Saved display'}</span><h2>${escapeHTML(device.label)}</h2></div><span class="status-badge ${device.connected ? 'good' : ''}">${device.connected ? 'Connected' : 'Disconnected'}</span></div>
    <div class="form-rows">
      <label class="form-row switch-row ${device.panel ? 'disabled' : ''}">
        <span><strong>Session approval</strong><small>${device.panel ? 'Built-in displays are always available' : 'Saved approval allows this display to trigger docking after sign-in; it does not update pre-login protection'}</small></span>
        <input type="checkbox" data-approve="${escapeAttr(device.deviceId)}" ${device.panel || device.sessionApproved ? 'checked' : ''} ${device.panel ? 'disabled' : ''} />
        <span class="switch" aria-hidden="true"></span>
      </label>
      <div class="form-row approval-detail"><span><strong>Pre-login enrollment</strong><small>${device.bootEligible ? 'Root-owned policy used before the graphical session starts' : 'Only stable EDID identities can be enrolled before login'}</small></span><span class="approval-state ${device.bootEnrolled ? 'approved' : ''}"><i data-lucide="${device.bootEnrolled ? 'lock-keyhole' : 'lock-open'}"></i>${device.bootEnrolled ? 'Enrolled' : 'Not enrolled'}</span></div>
      ${device.bootEnrolled && !device.sessionApproved ? `<div class="boot-revoke"><i data-lucide="shield-alert"></i><span><strong>This device remains in pre-login protection</strong><small>Revoke it from the saved session policy and authenticate to remove it now.</small></span><button class="secondary-button" data-boot-revoke="${escapeAttr(device.deviceId)}" ${busy ? 'disabled' : ''}><i data-lucide="user-x"></i><span>Revoke and sync</span></button></div>` : ''}
      ${output ? `
        <label class="form-row"><span><strong>Resolution</strong><small>Mode applied when this display is active</small></span>
          <select data-rule="mode">
            <option value="preferred" ${rule.mode === 'preferred' ? 'selected' : ''}>Preferred</option>
            ${modes.map((mode) => `<option value="${escapeAttr(mode.id)}" ${rule.mode === mode.id ? 'selected' : ''}>${formatMode(mode)}</option>`).join('')}
          </select>
        </label>
        <label class="form-row"><span><strong>Scale</strong><small>Interface scale on this display</small></span>
          <select data-rule="scale">${scaleOptions(rule.scale).map((value) => `<option value="${value}" ${Number(rule.scale) === value ? 'selected' : ''}>${Math.round(value * 100)}%</option>`).join('')}</select>
        </label>
        <label class="form-row"><span><strong>Position</strong><small>Placement relative to the primary display</small></span>
          <select data-rule="position">${optionList([['left', 'Left'], ['right', 'Right'], ['above', 'Above'], ['below', 'Below']], rule.position)}</select>
        </label>
        <label class="form-row"><span><strong>Primary priority</strong><small>Higher values win when multiple externals are connected</small></span>
          <input type="number" min="0" max="100" step="1" value="${Number(rule.primaryRank) || 0}" data-rule="primaryRank" />
        </label>
      ` : '<div class="disconnected-note"><i data-lucide="monitor-off"></i><span>Layout controls become available when this display reconnects.</span></div>'}
      <div class="form-row fingerprint"><span><strong>Device fingerprint</strong><small>${device.bootEligible ? 'Stable EDID identity' : 'Session-only identity'}</small></span><code>${escapeHTML(device.deviceId)}</code></div>
    </div>
  `;
}

function renderAutomation() {
  const preview = outcomePreview();
  return `
    <div class="page-heading">
      <div><span class="eyebrow">Policies</span><h1>Docking automation</h1></div>
      <button class="primary-button" data-action="save" ${!dirty || busy ? 'disabled' : ''}><i data-lucide="save"></i><span>Save changes</span></button>
    </div>
    <div class="automation-grid">
      <div class="automation-main">
        <section class="surface form-section">
          <div class="section-heading"><div><span class="eyebrow">After sign-in</span><h2><i data-lucide="rocket"></i>Login-session startup</h2></div></div>
          <div class="form-rows">
            <label class="form-row switch-row ${state.startup?.error ? 'disabled' : ''}">
              <span><strong>Start Dockstar when I sign in</strong><small>Launch the user-session controller and tray. This does not configure pre-login sleep protection or hardware wake.</small></span>
              <input type="checkbox" data-startup-toggle ${state.startup?.enabled ? 'checked' : ''} ${state.startup?.error || busy ? 'disabled' : ''} />
              <span class="switch" aria-hidden="true"></span>
            </label>
          </div>
          ${state.startup?.error ? `<div class="error-message"><i data-lucide="circle-alert"></i><span>${escapeHTML(state.startup.error)}</span></div>` : ''}
        </section>
        <section class="surface form-section">
          <div class="section-heading"><h2><i data-lucide="scan-search"></i>Detection</h2></div>
          <div class="form-rows">
            ${selectRow('Detection policy', 'Which external displays trigger Dockstar', 'detection', [['allowlist', 'Approved displays only'], ['all', 'All external displays'], ['off', 'Automation off']], draft.detection)}
            ${selectRow('Default layout', 'Applied when docking with the lid open', 'defaultLayout', layoutOptions.map(([value, , label]) => [value, label]), draft.defaultLayout)}
            ${selectRow('When lid is closed', 'Layout applied while docking closed', 'lidClosedLayout', layoutOptions.map(([value, , label]) => [value, label]), draft.lidClosedLayout)}
          </div>
        </section>
        <section class="surface form-section">
          <div class="section-heading"><h2><i data-lucide="sliders-horizontal"></i>System coordination</h2></div>
          <div class="form-rows">
            ${selectRow('Audio output', 'Preferred audio behavior while docked', 'audio.policy', [['prefer-display', 'Prefer display audio'], ['unchanged', 'Leave unchanged']], draft.audio.policy, 'volume-2')}
            ${checkRow('Restore previous audio on undock', 'audio.restore', draft.audio.restore)}
            ${selectRow('Network', 'Preferred connection while docked', 'network.policy', [['prefer-wired', 'Prefer wired'], ['wired-only', 'Wired only'], ['unchanged', 'Leave unchanged']], draft.network.policy, 'network')}
            ${selectRow('AC power profile', 'Power profile used while docked on AC', 'power.onAcProfile', [['performance', 'Performance'], ['balanced', 'Balanced'], ['power-saver', 'Power saver'], ['unchanged', 'Leave unchanged']], draft.power.onAcProfile, 'gauge')}
            ${checkRow('Restore previous power profile on undock', 'power.restore', draft.power.restore)}
            ${checkRow('Keep computer awake while docked', 'power.keepAwakeWhileDocked', draft.power.keepAwakeWhileDocked, 'Prevent automatic lid and idle sleep for approved docks')}
          </div>
        </section>
        <section class="surface form-section">
          <div class="section-heading"><h2><i data-lucide="shield-check"></i>Closed-lid undock</h2></div>
          <div class="form-rows">
            ${checkRow('Lock session', 'security.lockOnClosedUndock', draft.security.lockOnClosedUndock, 'Lock immediately after a closed-lid undock')}
            ${checkRow('Suspend computer', 'security.suspendOnClosedUndock', draft.security.suspendOnClosedUndock, 'Suspend after the session is locked', !draft.security.lockOnClosedUndock)}
          </div>
        </section>
      </div>
      <aside class="surface outcome-inspector">
        <div class="section-heading"><div><span class="eyebrow">Live outcome</span><h2>What happens next</h2></div><span class="status-badge good">Ready</span></div>
        <h3><i data-lucide="plug-zap"></i>Next dock</h3>
        <ol class="outcome-list">${preview.dock.map((item) => `<li>${escapeHTML(item)}</li>`).join('')}</ol>
        <div class="inspector-divider"></div>
        <h3><i data-lucide="unplug"></i>Next undock</h3>
        <ol class="outcome-list warm">${preview.undock.map((item) => `<li>${escapeHTML(item)}</li>`).join('')}</ol>
      </aside>
    </div>
  `;
}

function renderWake() {
  const wake = state.wake;
  const protection = sleepProtectionSummary();
  const support = state.wakeSupport || { manageable: false, persistent: false, missing: [] };
  const enabled = support.manageable && wake.installed && wake.enabled;
  const outOfSync = enabled && wake.sources.some((source) => source.state !== 'enabled');
  const statusLabel = state.wakeError ? 'Unavailable' : !support.manageable ? 'Setup required' : outOfSync ? 'Needs apply' : enabled ? 'Enabled' : 'Disabled';
  const heading = !support.manageable ? 'System support is required' : outOfSync ? 'Wake policy needs reapplying' : enabled ? 'Dock wake is enabled' : 'Dock wake is disabled';
  const detail = !support.manageable
    ? 'Install Dockstar from a system package to add the privileged wake helper.'
    : `${wake.sources.length} eligible controller${wake.sources.length === 1 ? '' : 's'} detected${support.persistent ? ' · reapplied after resume' : ''}. Hardware wake is separate from login-session startup and pre-login sleep protection.`;
  return `
    <div class="page-heading">
      <div><span class="eyebrow">Resume hardware</span><h1>Wake & power</h1></div>
      <span class="status-badge ${enabled ? 'good' : ''}">${statusLabel}</span>
    </div>
    ${state.wakeError ? `<div class="banner error"><i data-lucide="circle-alert"></i><span>${escapeHTML(state.wakeError)}</span></div>` : ''}
    ${protection.warning ? `<div class="banner warning"><i data-lucide="triangle-alert"></i><span>${escapeHTML(protection.warning)}</span></div>` : ''}
    <div class="wake-grid">
      <section class="surface wake-summary">
        <div class="wake-hero-icon ${enabled && !outOfSync ? 'enabled' : ''}"><i data-lucide="power"></i></div>
        <div><span class="eyebrow">Hardware wake</span><h2>${heading}</h2><p>${detail}</p></div>
        <div class="wake-actions">
          <label class="wake-toggle ${!support.manageable || busy ? 'disabled' : ''}">
            <span><strong>Allow docks to wake this computer</strong><small>${outOfSync ? 'Policy is on; sources need applying' : enabled ? 'Hardware wake is active' : 'Hardware wake is off'}</small></span>
            <input type="checkbox" data-wake-toggle ${enabled ? 'checked' : ''} ${!support.manageable || busy ? 'disabled' : ''} />
            <span class="switch" aria-hidden="true"></span>
          </label>
          ${outOfSync ? `<button class="primary-button" data-wake="enable" ${busy ? 'disabled' : ''}><i data-lucide="refresh-cw"></i><span>Reapply</span></button>` : ''}
          <button class="quiet-button" data-wake="restore" ${!support.manageable || !wake.installed || busy ? 'disabled' : ''}><i data-lucide="rotate-ccw"></i><span>Restore baseline</span></button>
        </div>
        <div class="wake-protection">
          <i data-lucide="shield-check"></i>
          <span><strong>Automatic sleep protection</strong><small>${escapeHTML(protection.detail)}</small></span>
          <span class="status-badge ${protection.good ? 'good' : ''}">${escapeHTML(protection.label)}</span>
        </div>
        <div class="wake-setup suspend-action">
          <i data-lucide="moon-star"></i>
          <span><strong>Intentional suspend</strong><small>Release Dockstar protection for one suspend cycle</small></span>
          <button class="secondary-button" data-action="suspend-now" ${busy ? 'disabled' : ''}><i data-lucide="moon"></i><span>Suspend now</span></button>
        </div>
        ${!support.manageable ? `<div class="wake-setup"><i data-lucide="shield-alert"></i><span><strong>Packaged support is not installed</strong><small>Missing: ${escapeHTML((support.missing || []).join(', ') || 'system wake components')}</small></span><button class="secondary-button" data-action="refresh" ${busy ? 'disabled' : ''}><i data-lucide="refresh-cw"></i><span>Recheck</span></button></div>` : ''}
      </section>
      <section class="surface wake-sources">
        <div class="section-heading"><h2>Eligible wake sources</h2><span class="count-badge">${wake.sources.length}</span></div>
        <div class="source-table" role="table">
          <div class="source-head" role="row"><span>Controller</span><span>Class</span><span>State</span></div>
          ${wake.sources.length ? wake.sources.map((source) => `
            <div class="source-row" role="row">
              <span><i data-lucide="cable"></i><code>${escapeHTML(source.id)}</code></span>
              <span>${wakeClass(source.class)}</span>
              <span class="source-state ${source.state === 'enabled' ? 'enabled' : ''}"><i data-lucide="${source.state === 'enabled' ? 'circle-check' : 'circle'}"></i>${escapeHTML(source.state)}</span>
            </div>
          `).join('') : renderEmpty('cable', 'No eligible wake controllers detected')}
        </div>
      </section>
    </div>
  `;
}

function renderDiagnostics() {
  return `
    <div class="page-heading">
      <div><span class="eyebrow">Support</span><h1>Diagnostics</h1></div>
      <div class="button-group">
        <button class="secondary-button" data-action="copy-diagnostics" ${!diagnostics ? 'disabled' : ''}><i data-lucide="copy"></i><span>Copy</span></button>
        <button class="primary-button" data-action="load-diagnostics" ${busy ? 'disabled' : ''}><i data-lucide="refresh-cw"></i><span>Run diagnostics</span></button>
      </div>
    </div>
    <section class="surface diagnostics-surface">
      <div class="section-heading"><h2><i data-lucide="file-json"></i>Daemon report</h2><span class="status-badge ${state.status.lastError ? '' : 'good'}">${state.status.lastError ? 'Review' : 'Healthy'}</span></div>
      ${diagnostics ? `<pre>${escapeHTML(diagnostics)}</pre>` : renderEmpty('activity', 'Run diagnostics to inspect the current environment')}
    </section>
  `;
}

function renderTopology(selected = '') {
  const outputs = connectedOutputs();
  if (!outputs.length) return renderEmpty('monitor-off', 'No displays detected');
  return `<div class="topology ${outputs.length === 1 ? 'single' : ''}">
    ${outputs.map((output, index) => {
      const primary = state.status.docked && (state.status.primary === output.deviceId || state.status.primary === output.connector || output.priority === 1);
      return `<button class="screen ${output.panel ? 'laptop-screen' : 'external-screen'} ${selected === output.deviceId ? 'selected' : ''}" data-device="${escapeAttr(output.deviceId)}">
        ${primary ? '<span class="primary-badge"><i data-lucide="star"></i>Primary</span>' : ''}
        <span class="screen-number">${index + 1}</span>
        <span class="screen-name">${escapeHTML(output.label || output.connector)}</span>
        <small>${escapeHTML(output.connector)} · ${output.width} × ${output.height}</small>
      </button>`;
    }).join('<span class="topology-link" aria-hidden="true"></span>')}
  </div>`;
}

function statusRow(icon, label, value, good) {
  return `<div class="status-row"><i data-lucide="${icon}"></i><span><small>${label}</small><strong>${escapeHTML(value)}</strong></span><i class="status-check ${good ? 'good' : ''}" data-lucide="${good ? 'circle-check' : 'circle-alert'}"></i></div>`;
}

function selectRow(title, subtitle, path, options, selected, icon = '') {
  return `<label class="form-row"><span>${icon ? `<i data-lucide="${icon}"></i>` : ''}<span><strong>${title}</strong><small>${subtitle}</small></span></span><select data-config="${path}">${optionList(options, selected)}</select></label>`;
}

function checkRow(title, path, checked, subtitle = '', disabled = false) {
  return `<label class="form-row switch-row ${disabled ? 'disabled' : ''}"><span><strong>${title}</strong>${subtitle ? `<small>${subtitle}</small>` : ''}</span><input type="checkbox" data-config="${path}" ${checked ? 'checked' : ''} ${disabled ? 'disabled' : ''} /><span class="switch" aria-hidden="true"></span></label>`;
}

function renderEmpty(icon, message) {
  return `<div class="empty-state"><i data-lucide="${icon}"></i><span>${escapeHTML(message)}</span></div>`;
}

function renderBoot() {
  return `<div class="boot"><img src="/dockstar-icon.png" alt="" /><strong>Dockstar</strong><span>Connecting to the display controller…</span></div>`;
}

function renderFatal(error) {
  root.innerHTML = `<div class="fatal"><img src="/dockstar-icon.png" alt="" /><h1>Dockstar could not connect</h1><p>${escapeHTML(error?.message || String(error))}</p><button class="primary-button" id="retry"><i data-lucide="refresh-cw"></i><span>Retry</span></button></div>`;
  createIcons({ icons, attrs: { 'aria-hidden': 'true' } });
  document.getElementById('retry').addEventListener('click', () => window.location.reload());
}

function bindEvents() {
  document.querySelectorAll('[data-view]').forEach((button) => button.addEventListener('click', () => {
    activeView = button.dataset.view;
    window.history.replaceState(null, '', `#${activeView}`);
    render();
    if (activeView === 'diagnostics' && !diagnostics) loadDiagnostics();
  }));
  document.querySelectorAll('[data-device]').forEach((button) => button.addEventListener('click', () => {
    selectedDeviceID = button.dataset.device;
    if (activeView !== 'displays') activeView = 'displays';
    render();
  }));
  document.querySelectorAll('[data-layout]').forEach((button) => button.addEventListener('click', () => {
    draft.defaultLayout = button.dataset.layout;
    updateDirty();
    render();
  }));
  document.querySelectorAll('[data-config]').forEach((control) => control.addEventListener('change', () => {
    setPath(draft, control.dataset.config, control.type === 'checkbox' ? control.checked : control.value);
    if (control.dataset.config === 'security.lockOnClosedUndock' && !control.checked) {
      draft.security.suspendOnClosedUndock = false;
    }
    updateDirty();
    render();
  }));
  document.querySelectorAll('[data-rule]').forEach((control) => control.addEventListener('change', () => {
    const output = connectedOutputs().find((item) => item.deviceId === selectedDeviceID);
    const rule = ensureDisplayRule(output);
    const numeric = ['scale', 'primaryRank'].includes(control.dataset.rule);
    rule[control.dataset.rule] = numeric ? Number(control.value) : control.value;
    updateDirty();
    render();
  }));
  document.querySelectorAll('[data-approve]').forEach((control) => control.addEventListener('change', () => approveDisplay(control.dataset.approve, control.checked)));
  document.querySelectorAll('[data-boot]').forEach((button) => button.addEventListener('click', () => bootAction(button.dataset.boot)));
  document.querySelectorAll('[data-boot-revoke]').forEach((button) => button.addEventListener('click', () => revokeBootDevice(button.dataset.bootRevoke)));
  document.querySelectorAll('[data-wake]').forEach((button) => button.addEventListener('click', () => wakeAction(button.dataset.wake)));
  document.querySelectorAll('[data-wake-toggle]').forEach((toggle) => toggle.addEventListener('change', () => wakeAction(toggle.checked ? 'enable' : 'disable')));
  document.querySelectorAll('[data-startup-toggle]').forEach((toggle) => toggle.addEventListener('change', () => setStartupEnabled(toggle.checked)));
  document.querySelectorAll('[data-action]').forEach((button) => button.addEventListener('click', () => handleAction(button.dataset.action)));
}

async function handleAction(action) {
  if (action === 'refresh') return refresh();
  if (action === 'save') return runBusy(async () => {
    await saveConfig(false);
    showToast('Settings saved');
  });
  if (action === 'apply-layout') return applyLayout();
  if (action === 'suspend-now') return suspendNow();
  if (action === 'load-diagnostics') return loadDiagnostics();
  if (action === 'copy-diagnostics') return copyDiagnostics();
}

async function refresh() {
  await runBusy(async () => {
    state = await callBackend('GetState');
    resetDraft();
    showToast('Status refreshed');
  });
}

async function refreshQuietly() {
  if (busy || dirty) return;
  try {
    state = await callBackend('GetState');
    resetDraft();
    render();
  } catch (_) {
    // The visible state remains useful during a short daemon restart.
  }
}

async function saveConfig(renderAfter = true) {
  state = await callBackend('SaveConfig', draft, state.revision);
  resetDraft();
  if (renderAfter) render();
}

async function applyLayout() {
  await runBusy(async () => {
    if (dirty) await saveConfig(false);
    state = await callBackend('SetLayout', draft.defaultLayout);
    resetDraft();
    showToast(`${layoutLabel(state.status.layout || draft.defaultLayout)} applied`);
  });
}

async function approveDisplay(id, approved) {
  await runBusy(async () => {
    if (dirty) await saveConfig(false);
    state = await callBackend('ApproveDisplay', id, approved);
    resetDraft();
    const unsynced = !state.boot?.inSync;
    showToast(`${approved ? 'Session approval saved' : 'Session approval revoked'}${unsynced ? '; pre-login policy still needs explicit sync' : ''}`);
  });
}

async function wakeAction(action) {
  await runBusy(async () => {
    if (dirty) await saveConfig(false);
    state = await callBackend('WakeAction', action);
    resetDraft();
    showToast(action === 'restore' ? 'Wake baseline restored' : `Hardware wake ${action}d`);
  });
}

async function setStartupEnabled(enabled) {
  await runBusy(async () => {
    state = await callBackend('SetStartupEnabled', enabled);
    resetDraft();
    showToast(enabled ? 'Dockstar will start when you sign in' : 'Dockstar startup disabled');
  });
}

async function bootAction(action) {
  await runBusy(async () => {
    if ((action === 'enable' || action === 'sync') && dirty) await saveConfig(false);
    try {
      state = await callBackend('BootAction', action);
      resetDraft();
    } catch (error) {
      try {
        state = await callBackend('GetState');
        resetDraft();
      } catch (_) {
        // Keep the last known state when the daemon is also unavailable.
      }
      throw new Error(`Pre-login protection was not confirmed: ${errorMessage(error)} Current installed state was refreshed when possible.`);
    }
    if (state.bootError) {
      throw new Error(`The privileged helper returned, but the installed pre-login policy cannot be verified: ${state.bootError}`);
    }
    if (action === 'disable' && state.boot?.enabled) {
      throw new Error('The privileged helper returned, but pre-login protection still reports enabled.');
    }
    if ((action === 'enable' || action === 'sync') && !state.boot?.inSync) {
      throw new Error('The privileged helper returned, but the installed pre-login policy still differs from saved approvals.');
    }
    showToast(action === 'disable' || !state.boot?.enabled ? 'Pre-login sleep protection disabled' : action === 'enable' ? 'Pre-login sleep protection enabled' : 'Pre-login approvals synchronized');
  });
}

async function revokeBootDevice(id) {
  await runBusy(async () => {
    if (dirty) await saveConfig(false);
    state = await callBackend('ApproveDisplay', id, false);
    resetDraft();
    try {
      state = await callBackend('BootAction', 'sync');
      resetDraft();
    } catch (error) {
      try {
        state = await callBackend('GetState');
        resetDraft();
      } catch (_) {
        // The saved revocation remains visible even if status refresh fails.
      }
      throw new Error(`Session approval was revoked, but the pre-login removal was not confirmed: ${errorMessage(error)}`);
    }
    if (state.bootError) {
      throw new Error(`Session approval was revoked, but the installed pre-login policy cannot be verified: ${state.bootError}`);
    }
    if (!state.boot?.inSync) {
      throw new Error('Session approval was revoked, but the installed pre-login policy still reports pending changes.');
    }
    showToast(state.boot?.enabled ? 'Display revoked from session and pre-login protection' : 'Display revoked; pre-login protection is now disabled');
  });
}

async function suspendNow() {
  await runBusy(async () => {
    if (dirty) await saveConfig(false);
    const confirmed = await callBackend('SuspendNow');
    if (confirmed) showToast('Suspend requested');
  });
}

async function loadDiagnostics() {
  await runBusy(async () => {
    diagnostics = await callBackend('GetDiagnostics');
  });
}

async function copyDiagnostics() {
  try {
    await navigator.clipboard.writeText(diagnostics);
    showToast('Diagnostics copied');
  } catch (error) {
    showToast(error.message || String(error), true);
  }
}

async function runBusy(action) {
  if (busy) return;
  busy = true;
  render();
  try {
    await action();
  } catch (error) {
    showToast(error.message || String(error), true);
  } finally {
    busy = false;
    render();
  }
}

function showToast(message, error = false) {
  window.clearTimeout(toastTimer);
  toastMessage = message;
  toastError = error;
  const toast = document.querySelector('.toast');
  if (!toast) return;
  toast.textContent = message;
  toast.classList.toggle('error', error);
  toast.classList.add('visible');
  toastTimer = window.setTimeout(() => {
    toastMessage = '';
    toastError = false;
    document.querySelector('.toast')?.classList.remove('visible');
  }, 3200);
}

function updateDirty() {
  dirty = JSON.stringify(draft) !== JSON.stringify(state.config);
}

function connectedOutputs() {
  return (state.status.outputs || []).filter((output) => output.connected);
}

function approvalDevices() {
  const devices = new Map();
  const upsert = (id, values) => {
    if (!id) return;
    devices.set(id, { ...(devices.get(id) || { deviceId: id }), ...values });
  };
  for (const output of state.status.outputs || []) {
    upsert(output.deviceId, {
      output,
      label: output.label || output.connector || output.deviceId,
      connector: output.connector,
      panel: Boolean(output.panel),
      connected: Boolean(output.connected),
    });
  }
  for (const rule of state.config.displays || []) {
    upsert(rule.id, {
      savedRule: rule,
      label: rule.label || devices.get(rule.id)?.label || rule.id,
    });
  }
  for (const id of state.boot?.allowedEdids || []) {
    upsert(id, { bootEnrolled: true, label: devices.get(id)?.label || shortDeviceID(id) });
  }
  const enrolled = new Set(state.boot?.allowedEdids || []);
  return [...devices.values()].map((device) => ({
    ...device,
    label: device.label || shortDeviceID(device.deviceId),
    panel: Boolean(device.panel),
    connected: Boolean(device.connected),
    sessionApproved: Boolean(device.panel || device.savedRule?.allowed),
    bootEligible: isEDIDIdentity(device.deviceId),
    bootEnrolled: enrolled.has(device.deviceId),
  })).sort((left, right) =>
    Number(right.panel) - Number(left.panel)
    || Number(right.connected) - Number(left.connected)
    || left.label.localeCompare(right.label));
}

function isEDIDIdentity(id) {
  return /^edid:[0-9a-f]{64}$/.test(String(id));
}

function shortDeviceID(id) {
  return isEDIDIdentity(id) ? `EDID ${id.slice(5, 13)}…` : id;
}

function deviceName(id, devices = approvalDevices()) {
  return devices.find((device) => device.deviceId === id)?.label || shortDeviceID(id);
}

function errorMessage(error) {
  return error?.message || String(error);
}

function mockApprovedEDIDs() {
  return [...new Set((state.config.displays || [])
    .filter((rule) => rule.allowed && isEDIDIdentity(rule.id))
    .map((rule) => rule.id))]
    .sort();
}

function refreshMockBootDiff() {
  const desired = mockApprovedEDIDs();
  const installed = [...(state.boot.allowedEdids || [])].sort();
  const desiredEnabled = state.config.detection !== 'off' && state.config.power.keepAwakeWhileDocked && desired.length > 0;
  const desiredSet = new Set(desired);
  const installedSet = new Set(installed);
  state.boot.pendingAdditions = desired.filter((id) => !installedSet.has(id));
  state.boot.pendingRemovals = installed.filter((id) => !desiredSet.has(id));
  state.boot.policyMismatch = state.boot.enabled !== desiredEnabled
    || (state.boot.installed && (state.boot.detection !== (desiredEnabled ? 'allowlist' : 'off') || state.boot.keepAwakeWhileDocked !== desiredEnabled));
  state.boot.inSync = !state.boot.policyMismatch
    && state.boot.pendingAdditions.length === 0
    && state.boot.pendingRemovals.length === 0;
}

function displayRule(output) {
  return draft.displays.find((rule) => rule.id === output.deviceId) || {
    id: output.deviceId,
    label: output.label,
    allowed: Boolean(output.panel),
    primaryRank: Number(output.priority) || 0,
    scale: Number(output.scale) || 1,
    mode: output.currentModeId || 'preferred',
    position: output.panel ? 'left' : 'right',
  };
}

function ensureDisplayRule(output) {
  let rule = draft.displays.find((item) => item.id === output.deviceId);
  if (!rule) {
    rule = displayRule(output);
    draft.displays.push(rule);
  }
  return rule;
}

function currentMode(output) {
  return output.modes?.find((mode) => mode.id === output.currentModeId) || { width: output.width, height: output.height, refreshHz: 0 };
}

function uniqueModes(modes) {
  const seen = new Set();
  return modes.filter((mode) => {
    const key = `${mode.width}x${mode.height}@${Math.round(mode.refreshHz)}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function scaleOptions(current) {
  return [...new Set([0.75, 1, 1.25, 1.5, 1.75, 2, Number(current)])]
    .filter((value) => Number.isFinite(value) && value >= 0.5 && value <= 4)
    .sort((left, right) => left - right);
}

function formatMode(mode) {
  if (!mode) return 'Unknown mode';
  const refresh = Number(mode.refreshHz || mode.refresh);
  return `${mode.width} × ${mode.height}${refresh ? ` · ${Math.round(refresh)} Hz` : ''}`;
}

function layoutLabel(layout) {
  return layoutOptions.find(([value]) => value === layout)?.[2] || 'Not active';
}

function relativeTime(value) {
  if (!value) return 'Unavailable';
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000));
  if (seconds < 10) return 'Just now';
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  return new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function optionList(options, selected) {
  return options.map(([value, label]) => `<option value="${escapeAttr(value)}" ${value === selected ? 'selected' : ''}>${escapeHTML(label)}</option>`).join('');
}

function setPath(target, path, value) {
  const keys = path.split('.');
  const final = keys.pop();
  const parent = keys.reduce((current, key) => current[key], target);
  parent[final] = value;
}

function outcomePreview() {
  const external = connectedOutputs().find((output) => !output.panel && (draft.detection === 'all' || output.approved));
  const dock = [];
  if (draft.detection === 'off') dock.push('Automatic docking remains off');
  else if (!external) dock.push('Wait for an approved external display');
  else {
    dock.push(`${external.label || external.connector} becomes the external workspace`);
    dock.push(`Apply ${layoutLabel(draft.defaultLayout).toLowerCase()} layout`);
    if (draft.audio.policy === 'prefer-display') dock.push('Prefer HDMI / DisplayPort audio');
    if (draft.network.policy === 'prefer-wired') dock.push('Prefer a wired connection');
    if (draft.network.policy === 'wired-only') dock.push('Disable wireless networking');
    if (draft.power.onAcProfile !== 'unchanged') dock.push(`Use ${draft.power.onAcProfile.replace('-', ' ')} power profile`);
    if (draft.power.keepAwakeWhileDocked) dock.push('Prevent automatic sleep while docked');
  }
  const undock = [];
  if (draft.audio.restore) undock.push('Restore the previous audio output');
  if (draft.power.restore) undock.push('Restore the previous power profile');
  if (draft.security.lockOnClosedUndock) undock.push('Lock after a closed-lid undock');
  if (draft.security.suspendOnClosedUndock) undock.push('Suspend after locking');
  if (!undock.length) undock.push('Leave system settings unchanged');
  return { dock, undock };
}

function sleepProtectionSummary() {
  const protection = state.status.sleepProtection || {
    enabled: Boolean(draft?.power?.keepAwakeWhileDocked), active: false, phase: 'inactive', backend: 'none', degraded: false,
  };
  if (!protection.enabled) {
    return { label: 'Disabled', detail: 'Dockstar will follow the desktop sleep policy', good: false, warning: '' };
  }
  if (protection.phase === 'manual-suspend') {
    return { label: 'Suspending', detail: 'Protection is released for this suspend only', good: true, warning: '' };
  }
  if (protection.lastError && !protection.active) {
    return { label: 'Unavailable', detail: protection.lastError, good: false, warning: protection.lastError };
  }
  if (!protection.active) {
    return { label: 'Ready', detail: 'Activates automatically for an approved dock', good: true, warning: '' };
  }
  if (protection.phase === 'resume-grace') {
    return { label: 'Resume guard', detail: 'Waiting briefly for the approved dock to appear', good: true, warning: '' };
  }
  if (protection.phase === 'undock-grace') {
    return { label: 'Disconnect guard', detail: 'Keeping the session stable while the dock disconnects', good: true, warning: '' };
  }
  if (protection.phase === 'snapshot-grace') {
    return { label: 'Detection guard', detail: 'Display state is temporarily unknown; sleep remains blocked while detection recovers', good: true, warning: protection.lastError || '' };
  }
  return { label: 'Protected', detail: 'System sleep is blocked while this dock is active', good: true, warning: '' };
}

function wakeClass(value) {
	if (value === 'usb-root-hub') return 'USB root hub';
  if (String(value).startsWith('0x03')) return 'Display controller';
  if (String(value).startsWith('0x0c03')) return 'USB controller';
  return value || 'Controller';
}

function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>'"]/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[character]);
}

function escapeAttr(value) {
  return escapeHTML(value).replace(/`/g, '&#96;');
}

function demoState() {
  const now = new Date().toISOString();
  const connectedID = `edid:${'a'.repeat(64)}`;
  const disconnectedID = `edid:${'b'.repeat(64)}`;
  const revokedID = `edid:${'c'.repeat(64)}`;
  const removedID = `edid:${'d'.repeat(64)}`;
  return {
    revision: 8,
    config: {
      schemaVersion: 2, revision: 8, detection: 'allowlist', defaultLayout: 'extend', lidClosedLayout: 'external-only',
      mirror: { source: 'primary-external', fallbackMaxRefreshHz: 60 },
      audio: { policy: 'prefer-display', restore: true }, network: { policy: 'prefer-wired' },
      power: { onAcProfile: 'performance', restore: true, keepAwakeWhileDocked: true },
      security: { lockOnClosedUndock: true, suspendOnClosedUndock: true }, wake: { enabled: true },
      displays: [
        { id: connectedID, label: 'LG UltraFine', allowed: true, primaryRank: 10, scale: 1, mode: 'ext-4k', position: 'right' },
        { id: disconnectedID, label: 'Conference projector', allowed: true, primaryRank: 2, scale: 1, mode: 'preferred', position: 'right' },
        { id: revokedID, label: 'Retired desk dock', allowed: false, primaryRank: 0, scale: 1, mode: 'preferred', position: 'right' },
      ],
    },
    status: {
      docked: true, lidClosed: false, layout: 'extend', primary: connectedID, backend: 'KScreen', updated: now,
      sleepProtection: { enabled: true, active: true, phase: 'snapshot-grace', backend: 'logind', mode: 'block', acquiredAt: now, degraded: true, lastError: 'KScreen is refreshing its output snapshot' },
      outputs: [
        { id: 1, deviceId: 'connector:internal-panel', connector: 'eDP-1', label: 'Laptop display', panel: true, connected: true, enabled: true, approved: true, priority: 2, scale: 1.5, width: 2560, height: 1600, currentModeId: 'panel-native', modes: [{ id: 'panel-native', width: 2560, height: 1600, refreshHz: 120 }] },
        { id: 2, deviceId: connectedID, connector: 'DisplayPort-1', label: 'LG UltraFine', panel: false, connected: true, enabled: true, approved: true, priority: 1, scale: 1, width: 3840, height: 2160, currentModeId: 'ext-4k', modes: [{ id: 'ext-4k', width: 3840, height: 2160, refreshHz: 60 }, { id: 'ext-1440', width: 2560, height: 1440, refreshHz: 60 }] },
      ],
    },
    boot: {
      installed: true, enabled: true, detection: 'allowlist', keepAwakeWhileDocked: true,
      allowedEdids: [connectedID, revokedID, removedID],
      pendingAdditions: [disconnectedID], pendingRemovals: [revokedID, removedID], policyMismatch: false, inSync: false,
    },
    bootSupport: { manageable: true, missing: [] },
    wake: { installed: true, enabled: true, sources: [
      { id: 'pci:0000:00:0d.0', class: '0x0c0330', vendor: '0x8086', device: '0x9a13', state: 'enabled', managed: true },
      { id: 'pci:0000:03:00.0', class: '0x030000', vendor: '0x1002', device: '0x73df', state: 'enabled', managed: true },
    ] },
    wakeSupport: { manageable: true, persistent: true, missing: [] },
    startup: { enabled: true, serviceEnabled: true, trayEnabled: true },
  };
}

function demoDiagnostics() {
  return {
    backend: 'KScreen', configPath: '/home/user/.config/dockstar/config.json', desktop: 'KDE', waylandDisplay: 'wayland-0',
    status: state.status, boot: state.boot, bootSupport: state.bootSupport, wake: state.wake,
  };
}
