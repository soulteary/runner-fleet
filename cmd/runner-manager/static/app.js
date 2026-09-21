// Runner Fleet 管理界面。
//
// 这份文件从 templates/index.html 的 <script> 块里拆出来：原先 CSS、HTML、JS
// 挤在一个上千行的 Go 模板里，编辑器没有语法高亮，浏览器也没法缓存。
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
// 「上次检查于 2026-09-21T09:00:00Z」这种原样输出的 RFC3339，得在脑子里换算
// 成「多久以前」才有用——而这一栏真正要回答的就是「检查是不是卡住了」。
// 相对时间交给 Intl.RelativeTimeFormat：复数和各语言说法它自己处理，
// 不必为此再铺一套六种语言的文案。
function relativeTime(d, lang) {
  if (typeof Intl === 'undefined' || !Intl.RelativeTimeFormat) return '';
  try {
    const rtf = new Intl.RelativeTimeFormat(lang || 'en', { numeric: 'auto' });
    const diff = (d.getTime() - Date.now()) / 1000;
    const units = [['year', 31536000], ['month', 2592000], ['day', 86400], ['hour', 3600], ['minute', 60]];
    for (let i = 0; i < units.length; i++) {
      if (Math.abs(diff) >= units[i][1]) return rtf.format(Math.round(diff / units[i][1]), units[i][0]);
    }
    return rtf.format(Math.round(diff), 'second');
  } catch (_) {
    return '';
  }
}
// 相对时间放主行，准确时刻放下面一行小字：前者回答「是不是卡住了」，
// 后者用来和日志对时间。解析不了就原样显示，别把信息弄丢。
function renderTimestamp(el, iso) {
  el.textContent = '';
  if (!iso) { el.textContent = '—'; return; }
  const lang = document.documentElement.lang || 'en';
  const d = new Date(iso);
  const rel = isNaN(d.getTime()) ? '' : relativeTime(d, lang);
  if (!rel) { el.textContent = iso; return; }
  el.appendChild(document.createTextNode(rel));
  const abs = document.createElement('span');
  abs.className = 'ts-abs';
  // 跟着页面语言而不是浏览器 locale：界面已经切到中文了，
  // 下面这行还按系统 locale 显示会很割裂
  abs.textContent = d.toLocaleString(lang);
  el.appendChild(abs);
}
// 健康的 Runner 上这 5 行全是「—」，占掉三栏网格的一大半
const PROBE_ROW_IDS = ['vProbeErrorTypeRow', 'vProbeSuggestionRow', 'vProbeCheckCmdRow', 'vProbeFixCmdRow', 'vProbeErrorRow'];
function setProbeRowsVisible(on) {
  PROBE_ROW_IDS.forEach(function(id) {
    const row = document.getElementById(id);
    if (row) row.style.display = on ? '' : 'none';
  });
}
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
// ===== 弹窗的键盘可达性 =====
// 此前弹窗没有 role/aria-modal，焦点也不受约束：Tab 会一路跑到弹窗背后的表格和
// 表单上，读屏器把背景内容和弹窗混着念；关掉之后焦点落回 <body>，键盘用户得从
// 页面开头一路 Tab 回原来的位置。
const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';
let lastFocused = null;
function focusablesIn(root) {
  // offsetParent 为 null 即当前不可见（弹窗里大量按钮是按状态 display:none 的），
  // 把它们算进去会让 Tab 停在看不见的地方
  return Array.prototype.filter.call(root.querySelectorAll(FOCUSABLE), function(el) {
    return el.offsetParent !== null;
  });
}
function trapTab(e, root) {
  if (e.key !== 'Tab') return;
  const items = focusablesIn(root);
  if (!items.length) return;
  const first = items[0], last = items[items.length - 1];
  if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
  else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
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
  // 记在展示之前：一旦弹窗抢走焦点就问不出用户原来站在哪了
  if (!modal.classList.contains('show')) lastFocused = document.activeElement;
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
        var driftRow = document.getElementById('vDriftRow');
        document.getElementById('vDrift').textContent = data.container_drift || '';
        driftRow.style.display = data.container_drift ? '' : 'none';
        setRowWide('vDriftRow', data.container_drift);
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
        document.getElementById('vBusy').innerHTML = data.github_busy ? ' <span class="badge busy has-tip" tabindex="0" role="note">' + escapeHtml(t('badge.busy')) + '<span class="tip">' + escapeHtml(t('badge.busy_title')) + '</span></span>' : '';
        const probeError = resolveProbeError(data);
        setProbeRowsVisible(!!probeError);
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
        renderTimestamp(document.getElementById('vRegistrationCheckedAt'), data.registration_checked_at);
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
        renderTimestamp(document.getElementById('vGitHubCheckAt'), data.github_check_at);
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
  // 焦点送进弹窗，否则 Tab 仍从页面开头走，读屏器也还停在背景内容上
  const firstInModal = focusablesIn(modal)[0];
  if (firstInModal) firstInModal.focus();
}

function closeModal() {
  modal.classList.remove('show');
  // 焦点还给打开它的那个按钮；元素可能已随自动刷新被换掉，所以先确认还在文档里
  if (lastFocused && document.contains(lastFocused)) lastFocused.focus();
  lastFocused = null;
}

document.getElementById('modalClose').addEventListener('click', closeModal);
document.getElementById('modalCancelBtn').addEventListener('click', closeModal);
modal.addEventListener('click', (e) => { if (e.target === modal) closeModal(); });
modal.addEventListener('keydown', (e) => trapTab(e, modal));
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

async function removeRunner(name) {
  if (!name || !confirm(t('confirm_remove').replace('{{name}}', name))) return;
  try {
    const r = await fetch('/api/runners/' + encodeURIComponent(name), { method: 'DELETE' });
    const data = await r.json().catch(() => ({}));
    if (r.ok) location.reload();
    else alert(data.message || r.statusText);
  } catch (e) { alert(e.message); }
}
// 行内按钮走事件委托而不是逐个 addEventListener：自动刷新会整体替换 <tbody>，
// 直接绑在按钮上的监听在第一次刷新之后就全部失效了。
const runnerRowsBody = document.getElementById('runnerRowsBody');
runnerRowsBody.addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-name]');
  if (!btn || !runnerRowsBody.contains(btn)) return;
  const name = btn.getAttribute('data-name');
  if (!name) return;
  if (btn.classList.contains('btn-view')) openModal('view', name);
  else if (btn.classList.contains('btn-edit')) openModal('edit', name);
  else if (btn.classList.contains('btn-del')) removeRunner(name);
  else if (btn.classList.contains('btn-start')) runnerAction(name, 'start');
  else if (btn.classList.contains('btn-stop')) runnerAction(name, 'stop');
  // 重建会删掉容器再按当前配置建一个新的，正在跑的 Job 会被中断，所以先确认
  else if (btn.classList.contains('btn-recreate')) {
    if (confirm(fillVars(t('confirm_recreate'), { name: name }))) runnerAction(name, 'recreate');
  }
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
// ===== 列表自动刷新 =====
// 「忙碌中」和「GitHub ✓」本来就是后台约 5 分钟一次的检查结果，页面不刷新
// 就永远看不到它们变化，这两列在日常使用中等于是死的。
// 拉的是 /api/runner-rows 渲染好的 <tbody> 片段（和首屏同一个模板），
// 而不是 /api/runners 的 JSON 自己拼行——行里 GitHubYes/GitHubNo 这类三态
// 判断在 JS 里重写一遍，正是最容易把「空闲」显示成「忙碌中」的地方。
const REFRESH_MS = 15000;
const autoRefreshToggle = document.getElementById('autoRefreshToggle');
const refreshStatusEl = document.getElementById('refreshStatus');
let refreshTimer = null;
let refreshing = false;

function refreshPaused() {
  // 弹窗开着时刷新没意义（用户看的是弹窗里的数据）；标签页不可见时也不必打扰
  // 服务端——容器模式下每刷新一次都要对每台 runner 做一次 docker inspect。
  return !autoRefreshToggle.checked || document.hidden || modal.classList.contains('show');
}

async function refreshRows() {
  if (refreshing || refreshPaused()) return;
  refreshing = true;
  try {
    const r = await fetch('/api/runner-rows', { headers: { 'Accept': 'text/html' } });
    if (!r.ok) throw new Error(r.statusText);
    const html = await r.text();
    // 再判一次：请求在途的这段时间里用户可能已经打开弹窗或关掉了自动刷新
    if (!refreshPaused()) {
      runnerRowsBody.innerHTML = html;
      // 换回来的是一整批新行，筛选状态不会跟着过来——不重筛的话，
      // 每 15 秒筛掉的行就会自己冒回来
      applyFilter();
      refreshStatusEl.className = 'refresh-status';
      refreshStatusEl.textContent = fillVars(t('autorefresh.updated'), { time: new Date().toLocaleTimeString() });
    }
  } catch (_) {
    refreshStatusEl.className = 'refresh-status err';
    refreshStatusEl.textContent = t('autorefresh.failed');
  } finally {
    refreshing = false;
  }
}

autoRefreshToggle.addEventListener('change', function() {
  try { localStorage.setItem('runnerFleetAutoRefresh', this.checked ? '1' : '0'); } catch (_) {}
  if (this.checked) { refreshStatusEl.className = 'refresh-status'; refreshStatusEl.textContent = ''; refreshRows(); }
  else { refreshStatusEl.className = 'refresh-status'; refreshStatusEl.textContent = t('autorefresh.paused'); }
});
try {
  if (localStorage.getItem('runnerFleetAutoRefresh') === '0') autoRefreshToggle.checked = false;
} catch (_) {}
if (!autoRefreshToggle.checked) refreshStatusEl.textContent = t('autorefresh.paused');
// 标签页切回来时立刻补一次，不用干等下一个周期
document.addEventListener('visibilitychange', () => { if (!document.hidden) refreshRows(); });
refreshTimer = setInterval(refreshRows, REFRESH_MS);
// ===== 概览统计与筛选 =====
// 这是个 fleet 管理器，但列表此前是平铺的：20 台以上只能靠 Ctrl+F 找，
// 也看不出「几台在跑、几台挂了」。筹码既是统计，也是筛选入口。
const PREDICATES = {
  all: function() { return true; },
  running: function(r) { return r.running; },
  stopped: function(r) { return r.status === 'installed' && !r.running; },
  new: function(r) { return r.status === 'new'; },
  // 异常：目录没了、状态查不出来、或探测失败——这三种都得有人去看一眼
  problem: function(r) { return r.status === 'missing' || r.status === 'unknown' || r.probe; },
  drift: function(r) { return r.drift; }
};
const statsEl = document.getElementById('listStats');
const searchEl = document.getElementById('runnerSearch');
const filterCountEl = document.getElementById('filterCount');
const filterClearBtn = document.getElementById('filterClearBtn');
let activeFilter = 'all';

function readRows() {
  return Array.prototype.map.call(document.querySelectorAll('tr[data-name]'), function(tr) {
    return {
      tr: tr,
      name: (tr.dataset.name || '').toLowerCase(),
      target: (tr.dataset.target || '').toLowerCase(),
      status: tr.dataset.status || '',
      running: tr.dataset.running === '1',
      drift: tr.dataset.drift === '1',
      probe: tr.dataset.probe === '1'
    };
  });
}

function applyFilter() {
  const rows = readRows();
  const q = (searchEl.value || '').trim().toLowerCase();
  const pred = PREDICATES[activeFilter] || PREDICATES.all;
  let shown = 0;
  rows.forEach(function(r) {
    const hit = pred(r) && (!q || r.name.indexOf(q) >= 0 || r.target.indexOf(q) >= 0);
    r.tr.style.display = hit ? '' : 'none';
    if (hit) shown++;
  });
  // 计数始终按全部算，不跟着当前筛选走：否则点进「需处理」之后其余筹码
  // 全变成 0，就再也看不出全局了
  statsEl.querySelectorAll('.stat-chip').forEach(function(chip) {
    const n = rows.filter(PREDICATES[chip.dataset.filter] || PREDICATES.all).length;
    chip.querySelector('.n').textContent = n;
    chip.classList.toggle('has', n > 0);
    // 数量为 0 的筹码收起来，免得小规模部署下摆一排空筹码；
    // 当前选中的那个即便归零也要留着，否则筛完就找不到退出的入口
    chip.style.display = (n > 0 || chip.dataset.filter === 'all' || chip.dataset.filter === activeFilter) ? '' : 'none';
  });
  const filtering = activeFilter !== 'all' || q !== '';
  filterCountEl.textContent = filtering ? fillVars(t('filter.showing'), { shown: shown, total: rows.length }) : '';
  filterClearBtn.style.display = filtering ? 'inline-block' : 'none';
  // 每次重新取：这一行住在 runnerRows 片段里，自动刷新会把它整个换掉，
  // 加载时存下来的那个引用刷新之后就指向一个已经离开文档的节点了
  const noMatchRow = document.getElementById('noMatchRow');
  if (noMatchRow) noMatchRow.style.display = (rows.length > 0 && shown === 0) ? '' : 'none';
}

function setActiveChip(name) {
  activeFilter = name;
  statsEl.querySelectorAll('.stat-chip').forEach(function(c) { c.classList.toggle('active', c.dataset.filter === name); });
}
statsEl.addEventListener('click', function(e) {
  const chip = e.target.closest('.stat-chip');
  if (!chip) return;
  setActiveChip(chip.dataset.filter);
  applyFilter();
});
searchEl.addEventListener('input', applyFilter);
filterClearBtn.addEventListener('click', function() {
  searchEl.value = '';
  setActiveChip('all');
  applyFilter();
  searchEl.focus();
});
applyFilter();

document.getElementById('modalStartBtnFooter').addEventListener('click', () => runnerAction(document.getElementById('modalStartBtnFooter').getAttribute('data-name'), 'start'));
document.getElementById('modalStopBtnFooter').addEventListener('click', () => runnerAction(document.getElementById('modalStopBtnFooter').getAttribute('data-name'), 'stop'));
