const apiBase = window.location.origin;
const defaultPortKey = 'default';
const portStorageKey = 'proxypools:selectedPortKey';

let currentPortKey = loadStoredPortKey();
let portsState = [];

function loadStoredPortKey() {
  try {
    return normalizePortKey(window.localStorage.getItem(portStorageKey));
  } catch {
    return defaultPortKey;
  }
}

function persistPortKey(portKey) {
  try {
    window.localStorage.setItem(portStorageKey, normalizePortKey(portKey));
  } catch {
    // ignore storage failures
  }
}

function normalizePortKey(value) {
  return value || defaultPortKey;
}

function resolveSelectedPortKey(items, preferredKey) {
  const normalized = normalizePortKey(preferredKey);
  if ((items || []).some((item) => item.key === normalized)) {
    return normalized;
  }
  if ((items || []).some((item) => item.key === defaultPortKey)) {
    return defaultPortKey;
  }
  return items?.[0]?.key || defaultPortKey;
}

function getPortRecord(portKey) {
  const normalized = normalizePortKey(portKey);
  return portsState.find((item) => item.key === normalized) || null;
}

function portDisplayName(portKey) {
  const normalized = normalizePortKey(portKey);
  const port = getPortRecord(normalized);
  if (port?.name) {
    return port.name;
  }
  if (port?.key) {
    return port.key;
  }
  return normalized === defaultPortKey ? '默认入口' : normalized;
}

function scopedApiPath(portKey, suffix) {
  return `${apiBase}/api/ports/${encodeURIComponent(normalizePortKey(portKey))}${suffix}`;
}

function textValue(value, fallback = '-') {
  if (value === undefined || value === null || value === '') {
    return fallback;
  }
  return String(value);
}

function runtimeModeLabel(value) {
  const mapping = {
    single_active: '单活动节点模式',
    pool: '代理池模式',
  };
  return mapping[value] || textValue(value);
}

function poolAlgorithmLabel(value) {
  const mapping = {
    sequential: '顺序调度',
    random: '随机调度',
    balance: '均衡调度',
  };
  return mapping[value] || textValue(value);
}

function selectionModeLabel(value) {
  const mapping = {
    auto: '自动选择',
    manual_locked: '手动锁定',
  };
  return mapping[value] || textValue(value);
}

function eventTypeLabel(value) {
  const mapping = {
    subscription_refresh: '订阅刷新',
    manual_switch: '手动切换',
    manual_unlock: '解除锁定',
    node_disabled: '禁用节点',
    node_enabled: '启用节点',
    health_check_failed: '健康检查失败',
    health_check_failover: '健康检查切换',
    manual_locked_failover: '手动锁定保底切换',
    pool_sequential_rotate: '顺序调度轮转',
    pool_sequential_failover: '顺序调度故障切换',
    pool_random_rotate: '随机调度轮转',
    pool_random_failover: '随机调度故障切换',
    pool_balance_rotate: '均衡调度轮转',
    pool_balance_failover: '均衡调度故障切换',
    runtime_settings_updated: '模式设置更新',
  };
  return mapping[value] || textValue(value);
}

function switchReasonLabel(value) {
  if (value === undefined || value === null || value === '') {
    return '未发生切换';
  }
  const mapping = {
    initial_selection: '初始选择',
    subscription_refresh_reselect: '订阅刷新后重选',
  };
  return mapping[value] || eventTypeLabel(value);
}

function levelLabel(value) {
  const mapping = {
    info: '信息',
    warn: '警告',
    error: '错误',
  };
  return mapping[value] || textValue(value);
}

function extractPortKey(metadataJSON) {
  if (!metadataJSON) {
    return '';
  }
  try {
    const parsed = JSON.parse(metadataJSON);
    return parsed.port_key || '';
  } catch {
    return '';
  }
}

function createPanel(title, subtitle = '') {
  const panel = document.createElement('section');
  panel.className = 'panel';

  const header = document.createElement('div');
  header.className = 'panel__header';

  const titleWrap = document.createElement('div');
  const heading = document.createElement('h2');
  heading.className = 'panel__title';
  heading.textContent = title;
  titleWrap.append(heading);
  if (subtitle) {
    const desc = document.createElement('p');
    desc.className = 'panel__subtitle';
    desc.textContent = subtitle;
    titleWrap.append(desc);
  }

  const actions = document.createElement('div');
  actions.className = 'panel__actions';

  header.append(titleWrap, actions);
  panel.append(header);
  return { panel, actions };
}

function createInfoRow(label, value, fallback = '-') {
  const row = document.createElement('div');
  row.className = 'info-row';
  const strong = document.createElement('strong');
  strong.textContent = `${label}：`;
  row.append(strong, ` ${textValue(value, fallback)}`);
  return row;
}

function createStatCard(label, value, meta = '') {
  const card = document.createElement('article');
  card.className = 'stat-card';

  const labelEl = document.createElement('div');
  labelEl.className = 'stat-card__label';
  labelEl.textContent = label;

  const valueEl = document.createElement('div');
  valueEl.className = 'stat-card__value';
  valueEl.textContent = textValue(value);

  const metaEl = document.createElement('div');
  metaEl.className = 'stat-card__meta';
  metaEl.textContent = meta;

  card.append(labelEl, valueEl, metaEl);
  return card;
}

function createBadge(text, variant) {
  const badge = document.createElement('span');
  badge.className = `badge badge--${variant}`;
  badge.textContent = text;
  return badge;
}

function buildRuntimeMessage(data) {
  if (!data.subscription_configured) {
    return '当前未配置订阅，手动切换不可用';
  }
  if (!data.running) {
    return '当前运行时未启动，手动切换不可用';
  }
  return '';
}

async function updateRuntimeSettings(portKey, runtimeMode, poolAlgorithm) {
  const response = await fetch(scopedApiPath(portKey, '/runtime/settings'), {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ runtime_mode: runtimeMode, pool_algorithm: poolAlgorithm }),
  });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '更新运行时设置失败');
  }
}

async function unlockSelection(portKey) {
  const response = await fetch(scopedApiPath(portKey, '/runtime/unlock'), {
    method: 'POST',
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '解除手动锁定失败');
  }
}

async function setNodeManualDisabled(portKey, nodeID, disabled) {
  const action = disabled ? 'disable' : 'enable';
  const response = await fetch(scopedApiPath(portKey, `/nodes/${nodeID}/${action}`), {
    method: 'POST',
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '更新节点状态失败');
  }
}

async function switchNode(portKey, nodeID) {
  const response = await fetch(scopedApiPath(portKey, `/nodes/${nodeID}/switch`), {
    method: 'POST',
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '切换节点失败');
  }
}

async function refreshSubscription() {
  const response = await fetch(`${apiBase}/api/subscription/refresh`, {
    method: 'POST',
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '刷新订阅失败');
  }
  return response.json();
}

async function fetchPorts() {
  const response = await fetch(`${apiBase}/api/ports`, { headers: { Accept: 'application/json' } });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '加载入口列表失败');
  }
  return response.json();
}

async function fetchSubscription() {
  const response = await fetch(`${apiBase}/api/subscription`, { headers: { Accept: 'application/json' } });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '加载订阅失败');
  }
  return response.json();
}

async function fetchRuntime(portKey) {
  const response = await fetch(scopedApiPath(portKey, '/runtime'), { headers: { Accept: 'application/json' } });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '加载运行时失败');
  }
  return response.json();
}

async function fetchDispatcherStatus() {
  const response = await fetch(`${apiBase}/api/dispatcher`, { headers: { Accept: 'application/json' } });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '加载 dispatcher 状态失败');
  }
  return response.json();
}

async function fetchEvents() {
  const response = await fetch(`${apiBase}/api/events`, { headers: { Accept: 'application/json' } });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || '加载事件日志失败');
  }
  return response.json();
}

function renderPortSelector(items, selectedPortKey) {
  const container = document.getElementById('port-selector');
  if (!container) {
    return;
  }
  container.replaceChildren();

  const wrapper = document.createElement('div');
  wrapper.className = 'port-switcher';

  const label = document.createElement('div');
  label.className = 'port-switcher__label';
  label.textContent = '当前入口';

  const select = document.createElement('select');
  select.className = 'panel-select port-switcher__select';
  select.disabled = (items || []).length === 0;
  for (const item of items || []) {
    const option = document.createElement('option');
    option.value = item.key;
    option.textContent = `${textValue(item.name, item.key)}（${item.key}）`;
    option.selected = item.key === selectedPortKey;
    select.append(option);
  }

  const meta = document.createElement('div');
  meta.className = 'port-switcher__meta';
  const selectedPort = getPortRecord(selectedPortKey);
  if (selectedPort) {
    meta.textContent = `${textValue(selectedPort.http_listen)} · ${textValue(selectedPort.socks_listen)} · ${runtimeModeLabel(selectedPort.runtime_mode)} / ${poolAlgorithmLabel(selectedPort.pool_algorithm)}`;
  } else {
    meta.textContent = '未发现入口配置';
  }

  select.addEventListener('change', async () => {
    currentPortKey = resolveSelectedPortKey(items || [], select.value);
    persistPortKey(currentPortKey);
    await loadAll();
  });

  wrapper.append(label, select, meta);
  container.append(wrapper);
}

function renderDashboard(runtimeData) {
  const container = document.getElementById('dashboard');
  container.replaceChildren();

  const grid = document.createElement('section');
  grid.className = 'dashboard-grid';
  const selectedPort = getPortRecord(runtimeData.port_key);
  grid.append(
    createStatCard('当前入口', portDisplayName(runtimeData.port_key), selectedPort ? `${textValue(selectedPort.http_listen)} · ${textValue(selectedPort.socks_listen)}` : textValue(runtimeData.port_key)),
    createStatCard('运行模式', runtimeModeLabel(runtimeData.runtime_mode), poolAlgorithmLabel(runtimeData.pool_algorithm)),
    createStatCard('选择模式', selectionModeLabel(runtimeData.selection_mode), switchReasonLabel(runtimeData.last_switch_reason)),
    createStatCard('当前活动节点', runtimeData.current_active_node_id, textValue(runtimeData.last_switch_at, '未记录')),
    createStatCard('健康节点', runtimeData.healthy_nodes, `节点总数 ${textValue(runtimeData.total_nodes, '0')}`),
    createStatCard('运行状态', runtimeData.running ? '运行中' : '未运行', textValue(runtimeData.last_error, '最近无错误'))
  );

  container.append(grid);
}

function renderSubscriptionPanel(data) {
  const container = document.getElementById('subscription');
  container.replaceChildren();

  const { panel, actions } = createPanel('订阅管理', '查看主订阅状态并触发手动刷新。订阅对所有入口共享。');
  const refreshButton = document.createElement('button');
  refreshButton.type = 'button';
  refreshButton.className = 'panel-button';
  refreshButton.textContent = '手动刷新订阅';
  actions.append(refreshButton);

  const grid = document.createElement('div');
  grid.className = 'subscription-grid';
  grid.append(
    createInfoRow('订阅名称', data.name),
    createInfoRow('订阅地址', data.url),
    createInfoRow('启用状态', data.enabled ? '已启用' : '未启用'),
    createInfoRow('最近拉取时间', data.last_fetch_at),
    createInfoRow('最近拉取状态', data.last_fetch_status),
    createInfoRow('最近拉取错误', data.last_fetch_error, '无'),
    createInfoRow('最近新增节点数', data.last_added_nodes, '0'),
    createInfoRow('最近移除节点数', data.last_removed_nodes, '0')
  );

  const message = document.createElement('div');
  message.className = 'panel-message';

  refreshButton.addEventListener('click', async () => {
    message.textContent = '';
    refreshButton.disabled = true;
    try {
      await refreshSubscription();
      message.textContent = '订阅刷新成功';
      await loadAll();
    } catch (error) {
      message.textContent = error.message;
      refreshButton.disabled = false;
    }
  });

  panel.append(grid, message);
  container.append(panel);
}

function renderRuntimeSettings(runtimeData) {
  const container = document.getElementById('runtime-settings');
  container.replaceChildren();

  const selectedPortKey = normalizePortKey(runtimeData.port_key || currentPortKey);
  const { panel, actions } = createPanel('模式与运行设置', `统一管理 ${portDisplayName(selectedPortKey)} 的运行模式、调度策略与运行时配置。`);
  const saveButton = document.createElement('button');
  saveButton.type = 'button';
  saveButton.className = 'panel-button';
  saveButton.textContent = '保存模式设置';
  actions.append(saveButton);

  const unlockButton = document.createElement('button');
  unlockButton.type = 'button';
  unlockButton.className = 'panel-button panel-button--secondary';
  unlockButton.textContent = '解除手动锁定';
  unlockButton.disabled = runtimeData.selection_mode !== 'manual_locked';
  actions.append(unlockButton);

  const settingsGrid = document.createElement('div');
  settingsGrid.className = 'settings-grid';

  const runtimeModeWrap = document.createElement('label');
  runtimeModeWrap.className = 'info-row';
  runtimeModeWrap.innerHTML = '<strong>运行模式：</strong>';
  const runtimeModeSelect = document.createElement('select');
  runtimeModeSelect.className = 'panel-select';
  for (const [value, label] of [['single_active', '单活动节点模式'], ['pool', '代理池模式']]) {
    const option = document.createElement('option');
    option.value = value;
    option.textContent = label;
    option.selected = runtimeData.runtime_mode === value;
    runtimeModeSelect.append(option);
  }
  runtimeModeWrap.append(runtimeModeSelect);

  const poolAlgorithmWrap = document.createElement('label');
  poolAlgorithmWrap.className = 'info-row';
  poolAlgorithmWrap.innerHTML = '<strong>池调度策略：</strong>';
  const poolAlgorithmSelect = document.createElement('select');
  poolAlgorithmSelect.className = 'panel-select';
  for (const [value, label] of [['sequential', '顺序调度'], ['random', '随机调度'], ['balance', '均衡调度']]) {
    const option = document.createElement('option');
    option.value = value;
    option.textContent = label;
    option.selected = runtimeData.pool_algorithm === value;
    poolAlgorithmSelect.append(option);
  }
  poolAlgorithmWrap.append(poolAlgorithmSelect);

  settingsGrid.append(runtimeModeWrap, poolAlgorithmWrap);

  const overviewGrid = document.createElement('div');
  overviewGrid.className = 'overview-grid';
  overviewGrid.append(
    createInfoRow('入口标识', runtimeData.port_key),
    createInfoRow('入口名称', portDisplayName(selectedPortKey)),
    createInfoRow('选择模式', selectionModeLabel(runtimeData.selection_mode)),
    createInfoRow('当前活动节点', runtimeData.current_active_node_id),
    createInfoRow('运行中', runtimeData.running ? '是' : '否'),
    createInfoRow('需重启生效', runtimeData.restart_required ? '是' : '否'),
    createInfoRow('管理后台监听', runtimeData.admin_listen),
    createInfoRow('HTTP 入口监听', runtimeData.http_listen),
    createInfoRow('SOCKS5 入口监听', runtimeData.socks_listen),
    createInfoRow('健康检查监听', runtimeData.health_listen),
    createInfoRow('最近应用状态', runtimeData.last_apply_status),
    createInfoRow('最近应用时间', runtimeData.last_apply_at),
    createInfoRow('最近订阅时间', runtimeData.last_subscription_fetch_at),
    createInfoRow('最近订阅状态', runtimeData.last_subscription_status),
    createInfoRow('最近健康检查', runtimeData.last_health_check_at),
    createInfoRow('最近切换原因', switchReasonLabel(runtimeData.last_switch_reason)),
    createInfoRow('最近切换时间', runtimeData.last_switch_at),
    createInfoRow('最近错误', runtimeData.last_error, '无')
  );

  const message = document.createElement('div');
  message.className = 'panel-message';
  message.textContent = buildRuntimeMessage(runtimeData);

  saveButton.addEventListener('click', async () => {
    message.textContent = '';
    saveButton.disabled = true;
    try {
      await updateRuntimeSettings(selectedPortKey, runtimeModeSelect.value, poolAlgorithmSelect.value);
      message.textContent = '运行时设置已更新';
      await loadAll();
    } catch (error) {
      message.textContent = error.message;
      saveButton.disabled = false;
    }
  });

  unlockButton.addEventListener('click', async () => {
    message.textContent = '';
    unlockButton.disabled = true;
    try {
      await unlockSelection(selectedPortKey);
      message.textContent = '已恢复自动选择';
      await loadAll();
    } catch (error) {
      message.textContent = error.message;
      unlockButton.disabled = false;
    }
  });

  panel.append(settingsGrid, overviewGrid, message);
  container.append(panel);
}

function renderNodeManagement(runtimeData) {
  const container = document.getElementById('node-management');
  container.replaceChildren();

  const selectedPortKey = normalizePortKey(runtimeData.port_key || currentPortKey);
  const { panel } = createPanel('节点管理', `查看 ${portDisplayName(selectedPortKey)} 的节点健康状态并执行切换、启用、禁用等操作。`);

  const toolbar = document.createElement('div');
  toolbar.className = 'node-toolbar';
  const summary = document.createElement('div');
  summary.className = 'node-filter';
  summary.textContent = `${portDisplayName(selectedPortKey)} 当前共有 ${textValue(runtimeData.total_nodes, '0')} 个节点，健康节点 ${textValue(runtimeData.healthy_nodes, '0')} 个。`;
  toolbar.append(summary);

  const list = document.createElement('ul');
  list.className = 'node-list';
  const message = document.createElement('div');
  message.className = 'panel-message';

  const handleSwitch = async (button) => {
    message.textContent = '';
    button.disabled = true;
    try {
      await switchNode(selectedPortKey, button.dataset.nodeId);
      message.textContent = '节点切换成功';
      await loadAll();
    } catch (error) {
      message.textContent = error.message;
      button.disabled = false;
    }
  };

  const handleToggleDisabled = async (button, currentlyDisabled) => {
    message.textContent = '';
    button.disabled = true;
    try {
      await setNodeManualDisabled(selectedPortKey, button.dataset.nodeId, !currentlyDisabled);
      message.textContent = currentlyDisabled ? '节点已启用' : '节点已禁用';
      await loadAll();
    } catch (error) {
      message.textContent = error.message;
      button.disabled = false;
    }
  };

  for (const node of runtimeData.node_details || []) {
    const item = document.createElement('li');
    item.className = `node-item${node.is_active ? ' node-item--active' : ''}`;

    const header = document.createElement('div');
    header.className = 'node-item__header';

    const titleWrap = document.createElement('div');
    titleWrap.className = 'node-item__title';
    const strong = document.createElement('strong');
    strong.textContent = textValue(node.name, '未命名节点');
    titleWrap.append(strong);

    const badgeRow = document.createElement('div');
    badgeRow.className = 'badge-row';
    if (node.is_active) {
      badgeRow.append(createBadge('当前活动节点', 'active'));
    }
    badgeRow.append(createBadge(`状态：${textValue(node.state)}`, 'status'));
    badgeRow.append(createBadge(`层级：${textValue(node.tier)}`, 'status'));
    if (node.manual_disabled) {
      badgeRow.append(createBadge('手动禁用', 'disabled'));
    }
    titleWrap.append(badgeRow);

    const actions = document.createElement('div');
    actions.className = 'node-item__actions';

    const switchButton = document.createElement('button');
    switchButton.type = 'button';
    switchButton.className = 'node-switch-button';
    switchButton.dataset.nodeId = String(node.id);
    switchButton.textContent = '切换到此节点';
    switchButton.disabled = !runtimeData.subscription_configured || !runtimeData.running || node.is_active || node.manual_disabled || node.state !== 'active';
    if (!switchButton.disabled) {
      switchButton.addEventListener('click', () => handleSwitch(switchButton));
    }

    const toggleButton = document.createElement('button');
    toggleButton.type = 'button';
    toggleButton.className = 'panel-button panel-button--secondary';
    toggleButton.dataset.nodeId = String(node.id);
    toggleButton.textContent = node.manual_disabled ? '启用节点' : '禁用节点';
    toggleButton.disabled = node.is_active && !node.manual_disabled;
    if (!toggleButton.disabled) {
      toggleButton.addEventListener('click', () => handleToggleDisabled(toggleButton, node.manual_disabled));
    }

    actions.append(switchButton, toggleButton);
    header.append(titleWrap, actions);

    const endpoint = document.createElement('div');
    endpoint.className = 'node-item__meta';
    endpoint.textContent = `${textValue(node.protocol_type)} · ${textValue(node.server)}:${textValue(node.port)}`;

    const stats = document.createElement('div');
    stats.className = 'node-item__stats';
    stats.textContent = `延迟 ${textValue(node.latency_ms, '0')}ms · 分数 ${textValue(node.score, '0')} · 连续失败 ${textValue(node.consecutive_failures, '0')} · 冷却到 ${textValue(node.cooldown_until, '无')}`;

    item.append(header, endpoint, stats);
    list.append(item);
  }

  panel.append(toolbar, message, list);
  container.append(panel);
}

function renderLanePanel(runtimeData) {
  const container = document.getElementById('lane-management');
  if (!container) {
    return;
  }
  container.replaceChildren();

  const selectedPortKey = normalizePortKey(runtimeData.port_key || currentPortKey);
  const { panel } = createPanel('Lane 运行态', `查看 ${portDisplayName(selectedPortKey)} 下内部 lane 的分配情况。`);
  const lanes = Array.isArray(runtimeData.lane_details) ? runtimeData.lane_details : [];
  const summary = document.createElement('div');
  summary.className = 'node-filter';
  summary.textContent = `Lane 总数 ${textValue(runtimeData.lane_count, '0')}，就绪 ${textValue(runtimeData.ready_lane_count, '0')}。`;
  panel.append(summary);

  const list = document.createElement('ul');
  list.className = 'node-list';
  for (const lane of lanes) {
    const item = document.createElement('li');
    item.className = 'node-item';
    const title = document.createElement('div');
    title.className = 'node-item__header';
    const titleWrap = document.createElement('div');
    titleWrap.className = 'node-item__title';
    const strong = document.createElement('strong');
    strong.textContent = textValue(lane.lane_key, '未命名 lane');
    titleWrap.append(strong);
    const badgeRow = document.createElement('div');
    badgeRow.className = 'badge-row';
    badgeRow.append(createBadge(`协议：${textValue(lane.protocol)}`, 'status'));
    badgeRow.append(createBadge(`权重：${textValue(lane.weight, '1')}`, 'status'));
    badgeRow.append(createBadge(`状态：${textValue(lane.state)}`, lane.state === 'ready' ? 'active' : 'disabled'));
    titleWrap.append(badgeRow);
    title.append(titleWrap);

    const assignment = document.createElement('div');
    assignment.className = 'node-item__meta';
    assignment.textContent = `绑定节点 ${textValue(lane.assigned_node_id, '0')} · 最近切换 ${textValue(lane.last_switch_at, '未记录')}`;

    const meta = document.createElement('div');
    meta.className = 'node-item__stats';
    meta.textContent = `切换原因 ${textValue(lane.last_switch_reason, '无')} · 最近使用 ${textValue(lane.last_used_at, '未记录')} · 最近错误 ${textValue(lane.last_error_at, '无')} · 状态 ${textValue(lane.state, 'idle')}`;

    item.append(title, assignment, meta);
    list.append(item);
  }
  panel.append(list);
  container.append(panel);
}

function renderDispatcherStatus(data) {
  const container = document.getElementById('dispatcher-status');
  if (!container) {
    return;
  }
  container.replaceChildren();

  const { panel } = createPanel('统一入口 Dispatcher', '查看统一入口监听、算法与当前选中的目标入口。');
  const grid = document.createElement('div');
  grid.className = 'overview-grid';
  grid.append(
    createInfoRow('启用状态', data.enabled ? '已启用' : '未启用'),
    createInfoRow('调度算法', poolAlgorithmLabel(data.algorithm)),
    createInfoRow('HTTP 统一入口', data.http_listen, '未启用'),
    createInfoRow('SOCKS5 统一入口', data.socks_listen, '未启用'),
    createInfoRow('当前目标入口', data.selected_port_key ? portDisplayName(data.selected_port_key) : '', '未选择'),
    createInfoRow('当前目标 Lane', data.selected_lane_key, '未选择'),
    createInfoRow('目标健康节点数', data.selected_healthy_nodes, '0'),
    createInfoRow('目标分数', data.selected_score, '0')
  );
  panel.append(grid);
  container.append(panel);
}

function renderEventsPanel(data) {
  const container = document.getElementById('events');
  container.replaceChildren();

  const { panel } = createPanel('事件日志', '最近的重要操作、切换与健康检查事件。当前为全局日志，含入口元数据。');
  const list = document.createElement('ul');
  list.className = 'events-list';

  for (const item of data.items || []) {
    const li = document.createElement('li');
    li.className = 'event-item';

    const meta = document.createElement('div');
    meta.className = 'event-item__meta';
    const metadataPortKey = extractPortKey(item.metadata_json);
    const portText = metadataPortKey ? ` · 入口=${portDisplayName(metadataPortKey)}` : '';
    meta.textContent = `${textValue(item.created_at)} · ${levelLabel(item.level)} · ${eventTypeLabel(item.event_type)}${portText} · 关联节点=${textValue(item.related_node_id, '0')}`;

    const messageText = document.createElement('div');
    messageText.className = 'event-item__message';
    messageText.textContent = textValue(item.message, '');

    const details = document.createElement('div');
    details.className = 'event-item__details';
    details.textContent = `元数据=${textValue(item.metadata_json, '{}')}`;

    li.append(meta, messageText, details);
    list.append(li);
  }

  panel.append(list);
  container.append(panel);
}

async function loadAll() {
  const [portsData, subscriptionData, eventsData, dispatcherData] = await Promise.all([
    fetchPorts(),
    fetchSubscription(),
    fetchEvents(),
    fetchDispatcherStatus(),
  ]);
  portsState = Array.isArray(portsData.items) ? portsData.items : [];
  currentPortKey = resolveSelectedPortKey(portsState, currentPortKey);
  persistPortKey(currentPortKey);

  const runtimeData = await fetchRuntime(currentPortKey);
  currentPortKey = normalizePortKey(runtimeData.port_key || currentPortKey);
  persistPortKey(currentPortKey);

  renderPortSelector(portsState, currentPortKey);
  renderDashboard(runtimeData);
  renderDispatcherStatus(dispatcherData);
  renderSubscriptionPanel(subscriptionData);
  renderRuntimeSettings(runtimeData);
  renderLanePanel(runtimeData);
  renderNodeManagement(runtimeData);
  renderEventsPanel(eventsData);
}

loadAll().catch((error) => {
  const portSelector = document.getElementById('port-selector');
  const dashboard = document.getElementById('dashboard');
  const dispatcher = document.getElementById('dispatcher-status');
  const runtime = document.getElementById('runtime-settings');
  if (portSelector) {
    portSelector.textContent = `加载入口失败：${error.message}`;
  }
  if (dashboard) {
    dashboard.textContent = `加载后台失败：${error.message}`;
  }
  if (dispatcher) {
    dispatcher.textContent = `加载 dispatcher 失败：${error.message}`;
  }
  if (runtime) {
    runtime.textContent = `加载后台失败：${error.message}`;
  }
});
