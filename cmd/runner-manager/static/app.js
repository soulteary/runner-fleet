// Runner Fleet 管理界面。
//
// 这份文件从 templates/index.html 的 <script> 块里拆出来：原先 CSS、HTML、JS
// 挤在一个 1019 行的 Go 模板里，编辑器没有语法高亮，浏览器也没法缓存。
// 拆出来之后它不再过 Go 模板，{{name}} 这类占位符可以直接写成字面量，
// 不必再写成 {{"{{"}}name{{"}}"}} 去躲模板的分隔符。
//
// i18n 字典由页面内联的一小段脚本先行注入到 window.__I18N。
function t(key) { return (window.__I18N && window.__I18N[key]) || key; }
document.getElementById('langSelect').addEventListener('change', function() {
  var lang = this.value;
  document.cookie = 'lang=' + encodeURIComponent(lang) + ';path=/;max-age=31536000';
  location.reload();
});
// Parse GitHub config.sh command and fill add-runner form
function sanitizeRunnerName(s) {
  if (!s || typeof s !== 'string') return '';
  return s.replace(/\.\./g, '').replace(/[/\\]/g, '-').trim() || 'runner';
}

function parseGitHubConfigCommand(text) {
  if (!text || typeof text !== 'string') return { ok: false, message: t('parse.please_enter_command') };
  const trimmed = text.trim();
  const urlMatch = trimmed.match(/\-\-url\s+["']?([^\s"']+)["']?/);
  const tokenMatch = trimmed.match(/\-\-token\s+["']?([^\s"']+)["']?/);
  if (!urlMatch) return { ok: false, message: t('parse.no_url') };
  const url = urlMatch[1].trim();
  let parsedUrl;
  try {
    parsedUrl = new URL(url);
  } catch (e) {
    return { ok: false, message: t('parse.invalid_url') };
  }
  const pathParts = parsedUrl.pathname.replace(/^\/+|\/+$/g, '').split('/').filter(Boolean);
  let targetType = 'org';
  let target = '';
  let suggestedName = '';
  if (pathParts.length >= 2) {
    targetType = 'repo';
    target = pathParts[0] + '/' + pathParts[1];
    suggestedName = sanitizeRunnerName(pathParts[1]);
  } else if (pathParts.length === 1) {
    target = pathParts[0];
    suggestedName = sanitizeRunnerName(pathParts[0]);
  } else {
    return { ok: false, message: t('parse.cannot_parse') };
  }
  const token = tokenMatch ? tokenMatch[1].trim() : '';
  return {
    ok: true,
    target_type: targetType,
    target: target,
    registration_token: token,
    suggested_name: suggestedName || 'runner'
  };
}

document.getElementById('parseCommandBtn').addEventListener('click', function() {
  const input = document.getElementById('githubCommandInput');
  const msgEl = document.getElementById('parseMsg');
  const result = parseGitHubConfigCommand(input.value);
  msgEl.textContent = '';
  msgEl.className = 'parse-msg';
  if (!result.ok) {
    msgEl.className = 'parse-msg err';
    msgEl.textContent = result.message;
    return;
  }
  document.getElementById('addFormTargetType').value = result.target_type;
  document.getElementById('addFormTarget').value = result.target;
  document.getElementById('addFormToken').value = result.registration_token || '';
  if (result.suggested_name && !document.getElementById('addFormName').value) {
    document.getElementById('addFormName').value = result.suggested_name;
    document.getElementById('addFormPath').value = '';
    // 用仓库名自动填出来的名字最容易撞上已有 Runner，填完立刻查一次
    schedulePrecheck();
  }
  msgEl.className = 'parse-msg ok';
  var filled = t('parse.filled').replace('{{target_type}}', result.target_type).replace('{{target}}', result.target).replace('{{token_part}}', result.registration_token ? t('parse.filled_token') : '');
  msgEl.textContent = filled;
});

// ===== 添加前的冲突预检 =====
// 名字撞车的形态不止一种：配置里已有同名、规范化后撞容器名、安装目录被占、
// 磁盘上留着上次注册过的目录、宿主机上还挂着同名容器。等到提交才发现太晚了，
// 这里在输入时就问一次服务端（/api/runner-precheck），并在提交前再确认一次。
var PH_OPEN = '{{', PH_CLOSE = '}}';
function fillVars(str, vars) {
  Object.keys(vars).forEach(function(k) {
    str = str.split(PH_OPEN + k + PH_CLOSE).join(vars[k] == null ? '' : vars[k]);
  });
  return str;
}
var PRECHECK_KEYS = {
  name_taken: 'precheck.name_taken',
  container_name: 'precheck.container_name',
  install_dir: 'precheck.install_dir',
  dir_registered: 'precheck.dir_registered',
  dir_adopt: 'precheck.dir_adopt',
  dir_exists: 'precheck.dir_exists',
  container_exists: 'precheck.container_exists'
};
function conflictText(conflict, resp) {
  var key = PRECHECK_KEYS[conflict.type];
  if (!key) return conflict.message || '';
  return fillVars(t(key), {
    detail: conflict.detail || '',
    dir: (resp && resp.install_dir) || conflict.detail || '',
    container: (resp && resp.container_name) || conflict.detail || '',
    status: conflict.status || ''
  });
}
var nameCheckEl = document.getElementById('nameCheckMsg');
var nameInput = document.getElementById('addFormName');
var pathInput = document.getElementById('addFormPath');
var tokenInput = document.getElementById('addFormToken');
var precheckSeq = 0;

function clearNameCheck() {
  nameCheckEl.className = 'check-msg';
  nameCheckEl.textContent = '';
}
function renderPrecheck(resp) {
  nameCheckEl.textContent = '';
  if (!resp) { clearNameCheck(); return; }
  var worst = 'ok';
  (resp.conflicts || []).forEach(function(c) {
    if (c.level === 'error') worst = 'err';
    else if (c.level === 'warn' && worst !== 'err') worst = 'warn';
  });
  nameCheckEl.className = 'check-msg ' + worst;
  if (!resp.conflicts || resp.conflicts.length === 0) {
    var okLine = document.createElement('span');
    okLine.className = 'check-line';
    okLine.textContent = t('precheck.ok');
    nameCheckEl.appendChild(okLine);
    return;
  }
  resp.conflicts.forEach(function(c) {
    var line = document.createElement('span');
    line.className = 'check-line';
    line.textContent = conflictText(c, resp);
    if (c.fix_command) {
      line.appendChild(document.createTextNode(' '));
      var code = document.createElement('code');
      code.textContent = c.fix_command;
      line.appendChild(code);
    }
    nameCheckEl.appendChild(line);
  });
  if (resp.suggested_name) {
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'btn-suggest';
    btn.textContent = fillVars(t('precheck.use_suggested'), { name: resp.suggested_name });
    btn.addEventListener('click', function() {
      nameInput.value = resp.suggested_name;
      runPrecheck(nameInput.value, pathInput.value.trim(), false).then(renderPrecheck);
    });
    nameCheckEl.appendChild(btn);
  }
}
// 返回 null 表示这次没查成（接口不可用等），此时不挡提交，交给服务端兜底。
// guardStale：边输入边查时，后发的请求先回来就该丢掉旧结果；
// 而点建议名、点提交这类明确动作必须拿到结论，不能被后续输入顺手丢掉。
async function runPrecheck(name, path, guardStale) {
  if (!name) return null;
  var seq = ++precheckSeq;
  try {
    // has_token 只传「填没填」，不传 token 本身：目录里已注册过的 Runner，
    // 带 token 再注册会被 config.sh 拒绝（error），不带 token 只是接管（提示即可）
    var url = '/api/runner-precheck?name=' + encodeURIComponent(name) +
      (path ? '&path=' + encodeURIComponent(path) : '') +
      (tokenInput && tokenInput.value.trim() ? '&has_token=1' : '');
    var r = await fetch(url);
    if (!r.ok) return null;
    var data = await r.json();
    if (guardStale && seq !== precheckSeq) return null;
    return data;
  } catch (e) {
    return null;
  }
}
var precheckTimer = null;
function schedulePrecheck() {
  if (precheckTimer) clearTimeout(precheckTimer);
  var name = nameInput.value.trim();
  if (!name) { clearNameCheck(); return; }
  precheckTimer = setTimeout(function() {
    nameCheckEl.className = 'check-msg';
    nameCheckEl.textContent = t('precheck.checking');
    runPrecheck(name, pathInput.value.trim(), true).then(function(resp) {
      if (resp) renderPrecheck(resp); else clearNameCheck();
    });
  }, 400);
}
nameInput.addEventListener('input', schedulePrecheck);
pathInput.addEventListener('input', schedulePrecheck);
tokenInput.addEventListener('input', schedulePrecheck);

document.getElementById('addForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const body = {};
  fd.forEach((v, k) => { if (v) body[k] = v; });
  if (body.labelsStr) {
    body.labels = body.labelsStr.split(',').map(s => s.trim()).filter(Boolean);
    delete body.labelsStr;
  }
  const msgEl = document.getElementById('addMsg');
  const msgWrap = document.getElementById('addMsgWrap');
  const submitBtn = e.target.querySelector('button[type="submit"]');
  msgWrap.style.display = 'none';
  msgEl.innerHTML = t('msg.adding');
  msgEl.className = 'msg';
  msgWrap.style.display = 'block';
  if (submitBtn) submitBtn.disabled = true;
  // 输入框上的提示可能已经过期（别处刚加了同名 Runner），提交前再确认一次
  const pre = await runPrecheck(body.name, body.path || '', false);
  if (pre && !pre.available) {
    renderPrecheck(pre);
    msgEl.className = 'msg err';
    msgEl.textContent = t('precheck.blocked');
    msgWrap.style.display = 'block';
    if (submitBtn) submitBtn.disabled = false;
    return;
  }
  try {
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 5 * 60 * 1000);
    const r = await fetch('/api/runners', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      signal: controller.signal
    });
    clearTimeout(timeoutId);
    const data = await r.json().catch(() => ({}));
    if (!r.ok) {
      // 409：服务端检出冲突，把冲突和建议名渲染到名称输入框下面，顺手给出「使用建议名」
      if (r.status === 409 && data.conflicts) {
        renderPrecheck(data);
        msgEl.className = 'msg err';
        msgEl.textContent = t('precheck.blocked');
      } else {
        msgEl.className = 'msg err';
        msgEl.textContent = data.message || r.statusText || t('msg.request_failed');
      }
      if (submitBtn) submitBtn.disabled = false;
      msgWrap.style.display = 'block';
      return;
    }
    msgEl.className = 'msg ok';
    // message 与 install_dir 里都嵌着用户填的 Runner 名，必须转义：
    // 名称校验只拦 .. / \，`<img src=x onerror=...>` 这样的名字是收得进来的
    msgEl.innerHTML = escapeHtml(data.message || '') +
      (data.install_dir ? '<br><span class="path">' + escapeHtml(data.install_dir) + '</span>' : '');
    if (data.output) msgEl.innerHTML += '<pre style="margin-top:8px;font-size:12px">' + escapeHtml(data.output) + '</pre>';
    if (data.queued) {
      msgEl.innerHTML += '<br><span style="font-size:12px;color:var(--muted)">' + t('msg.reload_in_5s') + '</span>';
      setTimeout(function() { location.reload(); }, 5000);
    }
    e.target.reset();
    clearNameCheck();
    if (submitBtn) submitBtn.disabled = false;
    msgWrap.style.display = 'block';
  } catch (err) {
    if (submitBtn) submitBtn.disabled = false;
    msgEl.className = 'msg err';
    msgEl.textContent = err.name === 'AbortError' ? t('msg.timeout_refresh') : (err.message || t('msg.request_failed'));
    msgWrap.style.display = 'block';
  }
});
document.getElementById('addMsgClose').addEventListener('click', function() {
  document.getElementById('addMsgWrap').style.display = 'none';
  location.reload();
});
// 超过这个长度的值挤在三分之一栏里会折成一条细柱，不如铺满一行
const WIDE_ROW_CHARS = 40;
function setRowWide(rowId, text) {
  const row = document.getElementById(rowId);
  if (row) row.classList.toggle('span-all', (text || '').length > WIDE_ROW_CHARS);
}

function escapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}
function resolveProbeSuggestion(data) {
  if (data && data.probe && data.probe.suggestion) return data.probe.suggestion;
  return t('probe.default_suggestion');
}
function resolveProbeError(data) {
  if (data && data.probe && data.probe.error) return data.probe.error;
  return '';
}
function resolveProbeCheckCommand(data) {
  if (data && data.probe && data.probe.check_command) return data.probe.check_command;
  return 'docker compose ps && docker logs --tail=200 runner-manager';
}
function resolveProbeFixCommand(data) {
  if (data && data.probe && data.probe.fix_command) return data.probe.fix_command;
  return 'docker compose up -d --force-recreate';
}
function resolveProbeType(data) {
  if (data && data.probe && data.probe.type) return data.probe.type;
  return 'unknown';
}
const modal = document.getElementById('runnerModal');
const modalTitle = document.getElementById('modalTitle');
const modalView = document.getElementById('modalView');
const modalEditForm = document.getElementById('modalEditForm');
const modalEditBtn = document.getElementById('modalEditBtn');
const modalSaveBtn = document.getElementById('modalSaveBtn');
const modalMsg = document.getElementById('modalMsg');
const revealFixBtn = document.getElementById('vRevealFixCmdBtn');
const copyCheckBtn = document.getElementById('vCopyCheckCmdBtn');
const copyFixBtn = document.getElementById('vCopyFixCmdBtn');
const probeFixCmdEl = document.getElementById('vProbeFixCommand');
const probeCheckCmdEl = document.getElementById('vProbeCheckCommand');
let currentProbeCheckCommand = '';
let currentProbeFixCommand = '';
let probeFixRevealed = false;

async function copyCommandText(text) {
  if (!text) return false;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch (_) {
    // ignore and fallback below
  }
  // 回退：在不支持/拒绝 clipboard API 的环境里尝试 execCommand
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.top = '-9999px';
    document.body.appendChild(ta);
    ta.select();
    ta.setSelectionRange(0, ta.value.length);
    const ok = document.execCommand && document.execCommand('copy');
    document.body.removeChild(ta);
    return !!ok;
  } catch (_) {
    return false;
  }
}

function openModal(mode, name) {
  modalMsg.style.display = 'none';
  if (mode === 'view') {
    modalTitle.textContent = t('modal.view_title');
    // 不写 'block'：行内样式会盖过样式表里的 display:grid，三栏布局就没了。
    // 置空即回落到样式表，栏数以后只在 CSS 里改一处。
    modalView.style.display = '';
    modalEditForm.style.display = 'none';
    modalEditBtn.style.display = 'inline-block';
    modalSaveBtn.style.display = 'none';
    document.getElementById('modalStartStopSpan').style.display = 'none';
    fetch('/api/runners/' + encodeURIComponent(name))
      .then(r => r.ok ? r.json() : Promise.reject(r))
      .then(data => {
        document.getElementById('vName').textContent = data.name || '';
        document.getElementById('vPath').textContent = data.path || data.name || '';
        document.getElementById('vTargetType').textContent = data.target_type || '';
        document.getElementById('vTarget').textContent = data.target || '';
        document.getElementById('vLabels').textContent = Array.isArray(data.labels) ? data.labels.join(', ') : (data.labels || '');
        document.getElementById('vInstallDir').textContent = data.install_dir || '';
        var jdbRow = document.getElementById('vJobDockerBackendRow');
        var jdbEl = document.getElementById('vJobDockerBackend');
        if (data.job_docker_backend) {
          jdbRow.style.display = '';
          jdbEl.textContent = data.job_docker_backend;
        } else {
          jdbRow.style.display = 'none';
        }
        document.getElementById('vStatus').innerHTML = '<span class="badge ' + escapeHtml(data.status || '') + '">' + escapeHtml(data.status || '') + '</span>';
        document.getElementById('vRunning').innerHTML = data.running ? ' <span class="badge running">' + t('badge.running') + '</span>' : '';
        document.getElementById('vBusy').innerHTML = data.github_busy ? ' <span class="badge busy" title="' + escapeHtml(t('badge.busy_title')) + '">' + escapeHtml(t('badge.busy')) + '</span>' : '';
        const probeError = resolveProbeError(data);
        document.getElementById('vProbeErrorType').textContent = probeError ? resolveProbeType(data) : '—';
        document.getElementById('vProbeSuggestion').textContent = probeError ? resolveProbeSuggestion(data) : '—';
        currentProbeCheckCommand = probeError ? resolveProbeCheckCommand(data) : '';
        probeCheckCmdEl.textContent = currentProbeCheckCommand || '—';
        copyCheckBtn.style.display = currentProbeCheckCommand ? 'inline-block' : 'none';
        currentProbeFixCommand = probeError ? resolveProbeFixCommand(data) : '';
        probeFixRevealed = false;
        probeFixCmdEl.textContent = probeError ? t('probe.fix_hidden_hint') : '—';
        revealFixBtn.style.display = probeError ? 'inline-block' : 'none';
        copyFixBtn.style.display = 'none';
        document.getElementById('vProbeError').textContent = probeError || '—';
        document.getElementById('vRegistrationMessage').textContent = data.registration_message || '—';
        setRowWide('vProbeSuggestionRow', probeError ? resolveProbeSuggestion(data) : '');
        setRowWide('vProbeCheckCmdRow', currentProbeCheckCommand);
        setRowWide('vProbeFixCmdRow', probeError ? resolveProbeFixCommand(data) : '');
        setRowWide('vProbeErrorRow', probeError);
        setRowWide('vRegistrationMessageRow', data.registration_message);
        document.getElementById('vRegistrationCheckedAt').textContent = data.registration_checked_at || '—';
        var gh = data.registered_on_github;
        var ghEl = document.getElementById('vRegisteredOnGitHub');
        var ghLink = document.getElementById('vGitHubLink');
        // 地址走 .href 属性而不是拼进 innerHTML：escapeHtml 管的是 HTML 上下文，
        // 挡不住 href 里的 javascript: 协议。地址本身也由 Go 侧按写死的
        // https://github.com 前缀拼出，两头都不给 target 改写协议的机会。
        ghLink.style.display = 'none';
        ghLink.removeAttribute('href');
        if (gh === true && data.github_url) {
          ghLink.href = data.github_url;
          ghLink.textContent = t('modal.gh_yes');
          ghLink.style.display = '';
          ghEl.textContent = '';
        }
        else if (gh === true) ghEl.innerHTML = '<span class="github-yes">' + t('modal.gh_yes') + '</span>';
        else if (gh === false) ghEl.innerHTML = '<span class="github-no">' + t('modal.gh_no') + '</span>';
        // gh 为 null：查过但没查出答案，和「从未查过」要分开说，否则过期的令牌看着像没配过
        else if (data.github_check_error) ghEl.textContent = t('modal.gh_failed') + '：' + data.github_check_error;
        else ghEl.textContent = t('modal.gh_unchecked');
        // 忙碌同样是三态：接口里没有这个字段就是「不知道」，不能说成「空闲」
        var busyEl = document.getElementById('vGitHubBusy');
        if (data.github_busy === true) busyEl.textContent = t('modal.gh_busy');
        else if (data.github_busy === false) busyEl.textContent = t('modal.gh_idle');
        else busyEl.textContent = t('modal.gh_busy_unknown');
        document.getElementById('vGitHubCheckAt').textContent = data.github_check_at || '—';
        const startStopSpan = document.getElementById('modalStartStopSpan');
        const startBtn = document.getElementById('modalStartBtnFooter');
        const stopBtn = document.getElementById('modalStopBtnFooter');
        startBtn.setAttribute('data-name', name);
        stopBtn.setAttribute('data-name', name);
        if (data.status === 'installed') {
          startStopSpan.style.display = 'inline-block';
          startBtn.style.display = data.running ? 'none' : 'inline-block';
          stopBtn.style.display = data.running ? 'inline-block' : 'none';
        } else if (data.status === 'unknown') {
          // 状态探测失败时允许用户手动尝试启停进行自愈
          startStopSpan.style.display = 'inline-block';
          startBtn.style.display = 'inline-block';
          stopBtn.style.display = 'inline-block';
        } else {
          startBtn.style.display = 'none';
          stopBtn.style.display = 'none';
        }
      })
      .catch(() => { modalMsg.textContent = t('msg.load_failed'); modalMsg.style.display = 'block'; modalMsg.className = 'msg err'; });
  } else {
    document.getElementById('modalStartStopSpan').style.display = 'none';
    modalTitle.textContent = t('modal.edit_title');
    modalView.style.display = 'none';
    modalEditForm.style.display = '';   // 同上，两栏由样式表决定
    modalEditBtn.style.display = 'none';
    modalSaveBtn.style.display = 'inline-block';
    fetch('/api/runners/' + encodeURIComponent(name))
      .then(r => r.ok ? r.json() : Promise.reject(r))
      .then(data => {
        document.getElementById('eName').value = data.name || '';
        document.getElementById('eNameDisplay').value = data.name || '';
        document.getElementById('ePath').value = data.path || '';
        document.getElementById('eTargetType').value = data.target_type || 'org';
        document.getElementById('eTarget').value = data.target || '';
        document.getElementById('eLabelsStr').value = Array.isArray(data.labels) ? data.labels.join(', ') : (data.labels || '');
      })
      .catch(() => { modalMsg.textContent = t('msg.load_failed'); modalMsg.style.display = 'block'; modalMsg.className = 'msg err'; });
  }
  modal.classList.add('show');
}

function closeModal() {
  modal.classList.remove('show');
}

document.getElementById('modalClose').addEventListener('click', closeModal);
document.getElementById('modalCancelBtn').addEventListener('click', closeModal);
modal.addEventListener('click', (e) => { if (e.target === modal) closeModal(); });
document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && modal.classList.contains('show')) closeModal(); });

document.getElementById('modalEditBtn').addEventListener('click', () => {
  const name = document.getElementById('vName').textContent;
  if (name) openModal('edit', name);
});

document.getElementById('modalSaveBtn').addEventListener('click', async () => {
  const name = document.getElementById('eName').value;
  if (!name) return;
  const labelsStr = document.getElementById('eLabelsStr').value.trim();
  const labels = labelsStr ? labelsStr.split(',').map(s => s.trim()).filter(Boolean) : [];
  const body = {
    name: name,
    path: document.getElementById('ePath').value.trim() || undefined,
    target_type: document.getElementById('eTargetType').value,
    target: document.getElementById('eTarget').value.trim(),
    labels: labels
  };
  modalMsg.style.display = 'none';
  try {
    const r = await fetch('/api/runners/' + encodeURIComponent(name), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) {
      modalMsg.className = 'msg err';
      modalMsg.textContent = data.message || r.statusText;
      modalMsg.style.display = 'block';
      return;
    }
    modalMsg.className = 'msg ok';
    modalMsg.textContent = data.message || t('msg.saved');
    modalMsg.style.display = 'block';
    setTimeout(() => { closeModal(); location.reload(); }, 800);
  } catch (e) {
    modalMsg.className = 'msg err';
    modalMsg.textContent = e.message;
    modalMsg.style.display = 'block';
  }
});

document.querySelectorAll('.btn-view').forEach(btn => {
  btn.addEventListener('click', () => openModal('view', btn.getAttribute('data-name')));
});
document.querySelectorAll('.btn-edit').forEach(btn => {
  btn.addEventListener('click', () => openModal('edit', btn.getAttribute('data-name')));
});

document.querySelectorAll('.btn-del').forEach(btn => {
  btn.addEventListener('click', async () => {
    const name = btn.getAttribute('data-name');
    if (!name || !confirm(t('confirm_remove').replace('{{name}}', name))) return;
    try {
      const r = await fetch('/api/runners/' + encodeURIComponent(name), { method: 'DELETE' });
      const data = await r.json().catch(() => ({}));
      if (r.ok) location.reload();
      else alert(data.message || r.statusText);
    } catch (e) { alert(e.message); }
  });
});

async function runnerAction(name, action) {
  try {
    const r = await fetch('/api/runners/' + encodeURIComponent(name) + '/' + action, { method: 'POST' });
    const data = await r.json().catch(() => ({}));
    if (r.ok) {
      const probeError = resolveProbeError(data);
      if (probeError) {
        const probeType = resolveProbeType(data);
        const checkMsg =
          (data.message || t('msg.action_done')) +
          '\n\n' + t('probe.alert_type') + probeType +
          '\n' + t('probe.alert_suggestion') + resolveProbeSuggestion(data) +
          '\n' + t('probe.alert_check_cmd') + resolveProbeCheckCommand(data) +
          '\n' + t('probe.alert_error') + probeError;
        alert(checkMsg);
        if (confirm(t('confirm_show_fix_cmd'))) {
          alert(t('probe.alert_fix_cmd') + '\n' + resolveProbeFixCommand(data));
        }
      }
      location.reload();
    }
    else { alert(data.message || r.statusText || t('msg.request_failed')); }
  } catch (e) { alert(e.message); }
}
revealFixBtn.addEventListener('click', () => {
  if (!currentProbeFixCommand) return;
  if (!confirm(t('confirm_reveal_fix'))) return;
  probeFixCmdEl.textContent = currentProbeFixCommand;
  probeFixRevealed = true;
  copyFixBtn.style.display = 'inline-block';
});
copyCheckBtn.addEventListener('click', async () => {
  if (!currentProbeCheckCommand) return;
  const ok = await copyCommandText(currentProbeCheckCommand);
  alert(ok ? t('msg.check_cmd_copied') : t('msg.copy_failed'));
});
copyFixBtn.addEventListener('click', async () => {
  if (!probeFixRevealed || !currentProbeFixCommand) {
    alert(t('msg.show_fix_first'));
    return;
  }
  const ok = await copyCommandText(currentProbeFixCommand);
  alert(ok ? t('msg.fix_cmd_copied') : t('msg.copy_failed'));
});
document.querySelectorAll('.btn-start').forEach(btn => {
  if (btn.id === 'modalStartBtnFooter') return;
  btn.addEventListener('click', () => runnerAction(btn.getAttribute('data-name'), 'start'));
});
document.querySelectorAll('.btn-stop').forEach(btn => {
  if (btn.id === 'modalStopBtnFooter') return;
  btn.addEventListener('click', () => runnerAction(btn.getAttribute('data-name'), 'stop'));
});
// 重建会删掉容器再按当前配置建一个新的，正在跑的 Job 会被中断，所以先确认
document.querySelectorAll('.btn-recreate').forEach(btn => {
  btn.addEventListener('click', () => {
    const name = btn.getAttribute('data-name');
    if (!name || !confirm(fillVars(t('confirm_recreate'), { name: name }))) return;
    runnerAction(name, 'recreate');
  });
});
document.getElementById('modalStartBtnFooter').addEventListener('click', () => runnerAction(document.getElementById('modalStartBtnFooter').getAttribute('data-name'), 'start'));
document.getElementById('modalStopBtnFooter').addEventListener('click', () => runnerAction(document.getElementById('modalStopBtnFooter').getAttribute('data-name'), 'stop'));
