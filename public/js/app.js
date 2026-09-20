(function () {
  'use strict';

  var $ = function (id) { return document.getElementById(id); };

  var state = {
    path: '/',
    unlocked: false,     // 是否已用超级码解锁；解锁状态在服务端 Cookie 里，保持 12 小时
    entries: [],
    cap: 20 * 1024 * 1024 * 1024,
    used: 0,
  };
  var toastTimer = null;
  var modalOnClose = null;

  /* ---------- 基础工具 ---------- */

  function toast(msg) {
    var el = $('toast');
    el.textContent = msg;
    el.classList.remove('hidden');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.classList.add('hidden'); }, 3000);
  }

  function esc(s) {
    return String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  function fmtBytes(v) {
    if (!isFinite(v) || v == null) return '--';
    var units = ['B', 'KB', 'MB', 'GB', 'TB'];
    var i = 0;
    var n = Number(v);
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return (n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)) + ' ' + units[i];
  }

  function fmtTime(ms) {
    if (!ms) return '';
    var d = new Date(ms);
    var p = function (n) { return n < 10 ? '0' + n : '' + n; };
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
  }

  function joinPath(dir, name) {
    return dir === '/' ? '/' + name : dir + '/' + name;
  }

  function parentPath(p) {
    if (!p || p === '/') return '/';
    var i = p.lastIndexOf('/');
    return i <= 0 ? '/' : p.slice(0, i);
  }

  function baseName(p) {
    var parts = String(p || '').split('/').filter(Boolean);
    return parts.length ? parts[parts.length - 1] : '';
  }

  function pathFromUrl() {
    try {
      var raw = new URLSearchParams(location.search).get('path');
      if (!raw) return '/';
      return raw.charAt(0) === '/' ? raw : '/' + raw;
    } catch (e) {
      return '/';
    }
  }

  async function api(path, opts) {
    opts = opts || {};
    opts.headers = opts.headers || {};
    // 鉴权走 Cookie（同源 fetch 默认带上），不再每个请求塞管理码头
    var res = await fetch(path, opts);
    var data = null;
    try { data = await res.json(); } catch (e) {}
    // Cookie 过期或被清掉：回到解锁页，而不是留一个空列表
    if (res.status === 401) {
      state.unlocked = false;
      applyLockedUi();
      gateHint('解锁状态已过期，请重新输入超级码');
    }
    if (!res.ok) throw new Error((data && data.error) || ('HTTP ' + res.status));
    return data;
  }

  function postJson(url, body) {
    return api(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  }

  /* ---------- 弹窗 ---------- */

  function openModal(opts) {
    var root = $('modal');
    root.className = 'modal' + (opts.variant ? ' ' + opts.variant : '');

    var title = $('modal-title');
    title.textContent = opts.title || '';
    title.classList.toggle('hidden', !opts.title);

    var body = $('modal-body');
    body.innerHTML = '';
    if (opts.body) body.appendChild(opts.body);

    var foot = $('modal-foot');
    foot.innerHTML = '';
    var buttons = (opts.actions || []).map(function (action) {
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'btn' + (action.kind ? ' ' + action.kind : '');
      btn.textContent = action.label;
      btn.addEventListener('click', function () { if (action.onClick) action.onClick(); });
      foot.appendChild(btn);
      return btn;
    });
    foot.classList.toggle('hidden', !buttons.length);

    modalOnClose = opts.onClose || null;
    root.classList.remove('hidden');
    document.body.classList.add('modal-open');
    if (opts.onMount) opts.onMount(buttons);
    return buttons;
  }

  function closeModal() {
    var root = $('modal');
    if (root.classList.contains('hidden')) return;
    root.classList.add('hidden');
    document.body.classList.remove('modal-open');
    $('modal-body').innerHTML = '';
    $('modal-foot').innerHTML = '';
    var cb = modalOnClose;
    modalOnClose = null;
    if (cb) cb();
  }

  function openSheet(title, options) {
    var list = document.createElement('div');
    list.className = 'sheet-list';
    options.forEach(function (opt) {
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'sheet-item' + (opt.danger ? ' danger' : '');
      btn.textContent = opt.label;
      btn.addEventListener('click', function () {
        closeModal();
        opt.onClick();
      });
      list.appendChild(btn);
    });
    openModal({
      title: title,
      body: list,
      variant: 'sheet',
      actions: [{ label: '取消', kind: 'ghost', onClick: closeModal }],
    });
  }

  // 用自研弹窗取代 window.prompt：
  // 移动端内置浏览器（飞书 / 微信等）会屏蔽原生 prompt，点了没反应也就改不了名
  function promptInput(opts) {
    return new Promise(function (resolve) {
      var input = document.createElement('input');
      input.className = 'input';
      input.type = opts.type || 'text';
      input.value = opts.value || '';
      if (opts.placeholder) input.placeholder = opts.placeholder;
      input.autocomplete = 'off';
      input.spellcheck = false;
      if (opts.maxlength) input.maxLength = opts.maxlength;

      var field = document.createElement('div');
      field.className = 'field';
      if (opts.label) {
        var label = document.createElement('label');
        label.className = 'field-label';
        label.textContent = opts.label;
        field.appendChild(label);
      }
      field.appendChild(input);
      if (opts.hint) {
        var hint = document.createElement('div');
        hint.className = 'field-hint';
        hint.textContent = opts.hint;
        field.appendChild(hint);
      }

      var settled = false;
      function finish(value) {
        if (settled) return;
        settled = true;
        closeModal();
        resolve(value);
      }

      openModal({
        title: opts.title,
        body: field,
        actions: [
          { label: '取消', kind: 'ghost', onClick: function () { finish(null); } },
          { label: opts.confirmLabel || '确定', kind: 'primary', onClick: function () { finish(input.value.trim()); } },
        ],
        onClose: function () { finish(null); },
        onMount: function () {
          try { input.focus(); } catch (e) {}
          var dot = input.value.lastIndexOf('.');
          // 默认选中主文件名（不含扩展名），手机上直接输入就能覆盖
          if (opts.type !== 'password' && dot > 0) {
            try { input.setSelectionRange(0, dot); return; } catch (e) {}
          }
          try { input.select(); } catch (e) {}
        },
      });

      input.addEventListener('keydown', function (ev) {
        if (ev.key === 'Enter') {
          ev.preventDefault();
          finish(input.value.trim());
        }
      });
    });
  }

  function confirmDialog(opts) {
    return new Promise(function (resolve) {
      var settled = false;
      function finish(value) {
        if (settled) return;
        settled = true;
        closeModal();
        resolve(value);
      }
      var text = document.createElement('p');
      text.className = 'modal-text';
      text.textContent = opts.message;
      openModal({
        title: opts.title,
        body: text,
        actions: [
          { label: '取消', kind: 'ghost', onClick: function () { finish(false); } },
          { label: opts.confirmLabel || '确定', kind: opts.danger ? 'danger solid' : 'primary', onClick: function () { finish(true); } },
        ],
        onClose: function () { finish(false); },
      });
    });
  }

  // 目录选择器：目标位置只用点选，比手打路径省事也不容易打错
  function pickDirectory(opts) {
    return new Promise(function (resolve) {
      var current = opts.start || '/';
      var settled = false;
      function finish(value) {
        if (settled) return;
        settled = true;
        closeModal();
        resolve(value);
      }

      var crumbsEl = document.createElement('div');
      crumbsEl.className = 'picker-crumbs';
      var listEl = document.createElement('div');
      listEl.className = 'picker-list';
      var box = document.createElement('div');
      box.className = 'picker';
      box.appendChild(crumbsEl);
      box.appendChild(listEl);

      var buttons = openModal({
        title: opts.title,
        body: box,
        actions: [
          { label: '取消', kind: 'ghost', onClick: function () { finish(null); } },
          { label: opts.confirmLabel || '确定', kind: 'primary', onClick: function () { finish(current); } },
        ],
        onClose: function () { finish(null); },
      });
      var confirmBtn = buttons[buttons.length - 1];
      confirmBtn.disabled = true;

      function blocked(dir) {
        // 移到原目录是空操作；移到自身内部服务端会报错，这里直接禁用
        if (opts.excludeDir && dir === opts.excludeDir) return true;
        if (opts.excludeTree && (dir === opts.excludeTree || dir.indexOf(opts.excludeTree + '/') === 0)) return true;
        return false;
      }

      function renderCrumbs() {
        crumbsEl.innerHTML = '';
        var rootBtn = document.createElement('button');
        rootBtn.type = 'button';
        rootBtn.className = 'crumb';
        rootBtn.textContent = '根目录';
        rootBtn.addEventListener('click', function () { load('/'); });
        crumbsEl.appendChild(rootBtn);

        var acc = '';
        current.split('/').filter(Boolean).forEach(function (seg) {
          acc += '/' + seg;
          var target = acc;
          var sep = document.createElement('span');
          sep.className = 'crumb-sep';
          sep.textContent = '›';
          crumbsEl.appendChild(sep);

          var btn = document.createElement('button');
          btn.type = 'button';
          btn.className = 'crumb' + (target === current ? ' current' : '');
          btn.textContent = seg;
          if (target !== current) btn.addEventListener('click', function () { load(target); });
          crumbsEl.appendChild(btn);
        });
        crumbsEl.scrollLeft = crumbsEl.scrollWidth;
      }

      function load(dir) {
        current = dir;
        renderCrumbs();
        listEl.innerHTML = '<div class="empty small">加载中…</div>';
        return api('/api/list?path=' + encodeURIComponent(dir)).then(function (data) {
          var dirs = (data.entries || []).filter(function (entry) { return entry.type === 'dir'; });
          listEl.innerHTML = '';
          if (!dirs.length) listEl.innerHTML = '<div class="empty small">没有子文件夹</div>';
          dirs.forEach(function (entry) {
            var btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'picker-item';
            var icon = document.createElement('span');
            icon.className = 'picker-item-icon';
            icon.textContent = '📁';
            var name = document.createElement('span');
            name.textContent = entry.name;
            btn.appendChild(icon);
            btn.appendChild(name);
            btn.addEventListener('click', function () { load(joinPath(current, entry.name)); });
            listEl.appendChild(btn);
          });
          confirmBtn.disabled = blocked(current);
        }).catch(function (err) {
          listEl.innerHTML = '<div class="empty small">加载失败：' + esc(err.message) + '</div>';
          confirmBtn.disabled = true;
        });
      }

      load(current);
    });
  }

  $('modal').addEventListener('click', function (ev) {
    if (ev.target.classList.contains('modal-mask')) closeModal();
  });
  document.addEventListener('keydown', function (ev) {
    if (ev.key === 'Escape' && !$('modal').classList.contains('hidden')) closeModal();
  });

  /* ---------- 导航 ---------- */

  function navigate(rel, push) {
    state.path = rel || '/';
    if (push) {
      var url = state.path === '/' ? location.pathname : location.pathname + '?path=' + encodeURIComponent(state.path);
      try { history.pushState({ path: state.path }, '', url); } catch (e) {}
    }
    loadList();
  }

  function renderCrumbs() {
    var el = $('crumbs');
    el.innerHTML = '';
    $('back-btn').classList.toggle('hidden', state.path === '/');

    var rootBtn = document.createElement('button');
    rootBtn.type = 'button';
    rootBtn.className = 'crumb' + (state.path === '/' ? ' current' : '');
    rootBtn.textContent = '根目录';
    if (state.path !== '/') rootBtn.addEventListener('click', function () { navigate('/', true); });
    el.appendChild(rootBtn);

    var acc = '';
    state.path.split('/').filter(Boolean).forEach(function (seg) {
      acc += '/' + seg;
      var target = acc;
      var sep = document.createElement('span');
      sep.className = 'crumb-sep';
      sep.textContent = '›';
      el.appendChild(sep);

      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'crumb' + (target === state.path ? ' current' : '');
      btn.textContent = seg;
      if (target !== state.path) btn.addEventListener('click', function () { navigate(target, true); });
      el.appendChild(btn);
    });
    el.scrollLeft = el.scrollWidth;
  }

  /* ---------- 列表渲染 ---------- */

  var EXT_ICON = {
    png: '🖼️', jpg: '🖼️', jpeg: '🖼️', gif: '🖼️', webp: '🖼️', bmp: '🖼️', svg: '🖼️', ico: '🖼️',
    mp4: '🎬', mkv: '🎬', mov: '🎬', avi: '🎬', webm: '🎬',
    mp3: '🎵', wav: '🎵', flac: '🎵', m4a: '🎵', ogg: '🎵',
    pdf: '📕', doc: '📘', docx: '📘', xls: '📗', xlsx: '📗', ppt: '📙', pptx: '📙',
    zip: '🗜️', rar: '🗜️', '7z': '🗜️', gz: '🗜️', tar: '🗜️',
    txt: '📝', md: '📝', log: '📝',
    js: '📜', json: '📜', css: '📜', html: '📜', py: '📜', sh: '📜',
  };

  function iconFor(name, isDir) {
    if (isDir) return '📁';
    var dot = String(name).lastIndexOf('.');
    var ext = dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
    return EXT_ICON[ext] || '📄';
  }

  function buildRow(item) {
    var isDir = item.type === 'dir';
    var rel = joinPath(state.path, item.name);

    var row = document.createElement('div');
    row.className = 'row';

    var icon = document.createElement('div');
    icon.className = 'row-icon';
    icon.textContent = iconFor(item.name, isDir);

    var nameWrap = document.createElement('div');
    nameWrap.className = 'row-name';
    var trigger;
    if (isDir) {
      trigger = document.createElement('button');
      trigger.type = 'button';
      trigger.className = 'name-btn';
      trigger.addEventListener('click', function (ev) {
        ev.stopPropagation();
        navigate(rel, true);
      });
    } else {
      trigger = document.createElement('a');
      trigger.className = 'name-link';
      trigger.href = downloadUrl(rel);
      trigger.setAttribute('download', item.name);
    }
    trigger.textContent = item.name;
    trigger.title = item.name;
    nameWrap.appendChild(trigger);

    var meta = document.createElement('div');
    meta.className = 'row-meta';
    var size = document.createElement('span');
    size.className = 'row-size';
    size.textContent = isDir ? '文件夹' : fmtBytes(item.size);
    var time = document.createElement('span');
    time.className = 'row-time';
    time.textContent = fmtTime(item.mtime);
    meta.appendChild(size);
    meta.appendChild(time);

    row.appendChild(icon);
    row.appendChild(nameWrap);
    row.appendChild(meta);

    if (state.unlocked) {
      var more = document.createElement('button');
      more.type = 'button';
      more.className = 'row-more';
      more.textContent = '⋯';
      more.title = '更多操作';
      more.setAttribute('aria-label', '更多操作');
      more.addEventListener('click', function (ev) {
        ev.stopPropagation();
        openRowSheet(item, rel);
      });
      row.appendChild(more);
    } else {
      var placeholder = document.createElement('div');
      placeholder.className = 'row-more-placeholder';
      row.appendChild(placeholder);
    }

    // 整行都可点：目录进入、文件下载，移动端可点面积更大
    row.addEventListener('click', function (ev) {
      if (ev.target.closest('.row-more') || ev.target.closest('.name-btn') || ev.target.closest('.name-link')) return;
      trigger.click();
    });
    return row;
  }

  function renderList(entries) {
    var listEl = $('list');
    if (!entries.length) {
      listEl.innerHTML = state.unlocked
        ? '<div class="empty">这里还没有文件<span class="empty-hint">把文件拖到这里，或点上方「上传」</span></div>'
        : '<div class="empty">这里还没有文件</div>';
      return;
    }
    listEl.innerHTML = '';
    var frag = document.createDocumentFragment();
    entries.forEach(function (item) { frag.appendChild(buildRow(item)); });
    listEl.appendChild(frag);
  }

  function renderUsage(used) {
    if (typeof used === 'number') state.used = used;
    var cap = state.cap > 0 ? state.cap : 1;
    var pct = Math.max(0, Math.min(100, state.used / cap * 100));
    $('usage-fill').style.width = pct + '%';
    $('usage-fill').classList.toggle('warn', pct >= 85);
    $('usage-text').textContent = '已用 ' + fmtBytes(state.used) + ' / ' + fmtBytes(state.cap);
  }

  async function loadList() {
    renderCrumbs();
    var listEl = $('list');
    listEl.innerHTML = '<div class="empty">加载中…</div>';
    try {
      var data = await api('/api/list?path=' + encodeURIComponent(state.path));
      state.entries = data.entries || [];
      renderUsage(data.usage);
      renderList(state.entries);
    } catch (err) {
      listEl.innerHTML = '<div class="empty">加载失败：' + esc(err.message) + '</div>';
    }
  }

  /* ---------- 行内操作 ---------- */

  function openRowSheet(item, rel) {
    var isDir = item.type === 'dir';
    var options = [
      { label: '重命名', onClick: function () { doRename(rel); } },
      { label: '移动到…', onClick: function () { doMoveCopy('move', rel, isDir); } },
      { label: '复制到…', onClick: function () { doMoveCopy('copy', rel, isDir); } },
    ];
    if (!isDir) {
      options.push({
        label: '下载',
        onClick: function () { location.href = downloadUrl(rel); },
      });
    }
    options.push({ label: '删除', danger: true, onClick: function () { doDelete(rel, item.name); } });
    openSheet(item.name, options);
  }

  async function doRename(rel) {
    var oldName = baseName(rel);
    var newName = await promptInput({
      title: '重命名',
      label: '新名称',
      value: oldName,
      confirmLabel: '重命名',
    });
    if (newName == null || !newName || newName === oldName) return;
    try {
      await postJson('/api/rename', { path: rel, newName: newName });
      toast('已重命名为 ' + newName);
      loadList();
    } catch (err) {
      toast(err.message);
    }
  }

  async function doMoveCopy(kind, rel, isDir) {
    var moving = kind === 'move';
    var label = baseName(rel);
    var destDir = await pickDirectory({
      title: (moving ? '移动「' : '复制「') + label + '」到',
      confirmLabel: moving ? '移动到这里' : '复制到这里',
      start: state.path,
      excludeDir: parentPath(rel),
      excludeTree: isDir ? rel : null,
    });
    if (destDir == null) return;
    try {
      await postJson('/api/' + kind, { path: rel, destDir: destDir });
      toast(moving ? '已移动' : '已复制');
      loadList();
    } catch (err) {
      toast(err.message);
    }
  }

  async function doDelete(rel, name) {
    var ok = await confirmDialog({
      title: '删除',
      message: '确定删除「' + name + '」？删除后无法恢复。',
      confirmLabel: '删除',
      danger: true,
    });
    if (!ok) return;
    try {
      await postJson('/api/delete', { path: rel });
      toast('已删除');
      loadList();
    } catch (err) {
      toast(err.message);
    }
  }

  /* ---------- 解锁与锁定 ---------- */

  // 下载是浏览器直接点链接，鉴权只能靠 Cookie（同一会话已解锁）
  function downloadUrl(rel) {
    return '/api/download?path=' + encodeURIComponent(rel);
  }

  // 未解锁时只显示解锁页，文件界面整块收起来，避免闪出空列表
  function applyLockedUi() {
    var locked = !state.unlocked;
    $('wrap').classList.toggle('hidden', locked);
    $('gate').classList.toggle('hidden', !locked);
    $('lock-btn').classList.toggle('hidden', locked);
    $('refresh-btn').classList.toggle('hidden', locked);
    $('toolbar-actions').classList.toggle('hidden', locked);
    document.body.classList.toggle('is-admin', !locked);
    document.body.classList.toggle('locked', locked);
  }

  function gateHint(msg) {
    var el = $('gate-hint');
    el.textContent = msg || '';
    el.classList.toggle('hidden', !msg);
  }

  async function unlock(code) {
    await postJson('/api/unlock', { code: code });
    state.unlocked = true;
    applyLockedUi();
    gateHint('');
    toast('已解锁');
    loadList();
  }

  async function lock() {
    try { await postJson('/api/lock'); } catch (e) {}
    state.unlocked = false;
    state.path = '/';
    state.entries = [];
    applyLockedUi();
    gateHint('已锁定，请重新输入超级码');
    $('gate-input').focus();
  }

  $('lock-btn').addEventListener('click', function () { lock(); });

  $('gate-form').addEventListener('submit', async function (ev) {
    ev.preventDefault();
    var input = $('gate-input');
    var code = input.value.trim();
    if (!code) return;
    var submit = $('gate-submit');
    submit.disabled = true;
    try {
      await unlock(code);
      input.value = '';
    } catch (err) {
      gateHint(err.message);
      input.select();
    } finally {
      submit.disabled = false;
    }
  });

  /* ---------- 上传 ---------- */

  function showProgress(text, ratio) {
    $('upload-box').classList.remove('hidden');
    $('upload-text').textContent = text;
    $('upload-fill').style.width = Math.max(0, Math.min(100, ratio * 100)) + '%';
  }

  function hideProgress() {
    $('upload-box').classList.add('hidden');
  }

  // 用 XHR 而不是 fetch，为了拿到上传进度
  function uploadOne(file, dir, onProgress) {
    return new Promise(function (resolve, reject) {
      var xhr = new XMLHttpRequest();
      // 凭 Cookie 放行，不额外带头
      xhr.open('POST', '/api/upload?path=' + encodeURIComponent(dir));
      xhr.setRequestHeader('X-File-Name', encodeURIComponent(file.name));
      xhr.upload.onprogress = function (ev) {
        if (ev.lengthComputable) onProgress(ev.loaded / ev.total);
      };
      xhr.onload = function () {
        var data = null;
        try { data = JSON.parse(xhr.responseText); } catch (e) {}
        if (xhr.status >= 200 && xhr.status < 300) resolve(data);
        else reject(new Error((data && data.error) || ('HTTP ' + xhr.status)));
      };
      xhr.onerror = function () { reject(new Error('网络中断')); };
      xhr.onabort = function () { reject(new Error('已取消')); };
      xhr.send(file);
    });
  }

  async function uploadFiles(fileList) {
    var files = Array.prototype.slice.call(fileList || []);
    if (!files.length) return;
    var dir = state.path;
    var total = files.length;
    var finished = 0;
    showProgress('准备上传…', 0);
    for (var i = 0; i < files.length; i++) {
      var file = files[i];
      var index = i;
      try {
        await uploadOne(file, dir, function (ratio) {
          showProgress('上传中 ' + (index + 1) + '/' + total + ' · ' + file.name, (finished + ratio) / total);
        });
        finished++;
      } catch (err) {
        hideProgress();
        toast('上传失败（' + file.name + '）：' + err.message);
        loadList();
        return;
      }
    }
    hideProgress();
    toast(total > 1 ? '已上传 ' + total + ' 个文件' : '已上传 ' + files[0].name);
    loadList();
  }

  $('upload-btn').addEventListener('click', function () { $('file-input').click(); });
  $('file-input').addEventListener('change', function () {
    // 必须先把文件拷进数组再清空 value：给 value 赋值会把 FileList 原地清空，
    // 直接传 input.files 会拿到空列表，表现为「选了文件但什么都没发生」
    var files = Array.prototype.slice.call($('file-input').files || []);
    $('file-input').value = '';
    uploadFiles(files);
  });

  var dragDepth = 0;
  function isFileDrag(ev) {
    var dt = ev.dataTransfer;
    return !!dt && Array.prototype.indexOf.call(dt.types || [], 'Files') >= 0;
  }
  document.addEventListener('dragover', function (ev) {
    if (!state.unlocked || !isFileDrag(ev)) return;
    ev.preventDefault();
    ev.dataTransfer.dropEffect = 'copy';
  });
  document.addEventListener('dragenter', function (ev) {
    if (!state.unlocked || !isFileDrag(ev)) return;
    ev.preventDefault();
    dragDepth++;
    $('drop-hint').classList.remove('hidden');
  });
  document.addEventListener('dragleave', function (ev) {
    if (!state.unlocked || !isFileDrag(ev)) return;
    dragDepth = Math.max(0, dragDepth - 1);
    if (!dragDepth) $('drop-hint').classList.add('hidden');
  });
  document.addEventListener('drop', function (ev) {
    if (!state.unlocked || !isFileDrag(ev)) return;
    ev.preventDefault();
    dragDepth = 0;
    $('drop-hint').classList.add('hidden');
    uploadFiles(ev.dataTransfer.files);
  });

  /* ---------- 其余控件 ---------- */

  $('mkdir-btn').addEventListener('click', async function () {
    var name = await promptInput({
      title: '新建文件夹',
      label: '文件夹名称',
      placeholder: '例如 图片',
      confirmLabel: '创建',
    });
    if (!name) return;
    try {
      await postJson('/api/mkdir', { path: state.path, name: name });
      toast('已创建 ' + name);
      loadList();
    } catch (err) {
      toast(err.message);
    }
  });

  $('back-btn').addEventListener('click', function () { navigate(parentPath(state.path), true); });
  $('refresh-btn').addEventListener('click', loadList);
  $('brand').addEventListener('click', function () { if (state.path !== '/') navigate('/', true); });

  window.addEventListener('popstate', function (ev) {
    state.path = (ev.state && ev.state.path) || pathFromUrl();
    loadList();
  });

  /* ---------- 启动 ---------- */

  state.path = pathFromUrl();
  try { history.replaceState({ path: state.path }, '', location.href); } catch (e) {}
  applyLockedUi();
  // 先问一次会话状态：已解锁就直接列目录，否则停在解锁页
  api('/api/session').then(function (data) {
    if (data && data.unlocked) {
      state.unlocked = true;
      applyLockedUi();
      loadList();
      api('/api/usage').then(function (usage) {
        if (usage && typeof usage.cap === 'number' && usage.cap > 0) state.cap = usage.cap;
        if (usage) renderUsage(typeof usage.used === 'number' ? usage.used : undefined);
      }).catch(function () {});
      return;
    }
    if (data && data.configured === false) {
      gateHint('服务端没有配置访问码，先设置 FILE_ADMIN_CODE 再重启服务');
    }
    $('gate-input').focus();
  }).catch(function (err) {
    gateHint('无法连接服务：' + err.message);
  });
})();
