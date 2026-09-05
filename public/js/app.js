(function () {
  'use strict';
  var $ = function (id) { return document.getElementById(id); };
  var state = { path: '/', admin: false, code: null };
  var toastTimer = null;

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
  function fullRel(child) {
    if (state.path === '/') return '/' + child;
    return state.path + '/' + child;
  }
  async function api(path, opts) {
    opts = opts || {};
    opts.headers = opts.headers || {};
    if (state.code) opts.headers['X-Admin-Code'] = state.code;
    var res = await fetch(path, opts);
    var data = null;
    try { data = await res.json(); } catch (e) {}
    if (!res.ok) throw new Error((data && data.error) || ('HTTP ' + res.status));
    return data;
  }

  function updateAdminUI() {
    $('admin-panel').classList.remove('hidden');
    $('code-form').classList.toggle('hidden', state.admin);
    $('admin-tools').classList.toggle('hidden', !state.admin);
    $('admin-toggle').textContent = state.admin ? '管理模式（已启用）' : '管理模式';
    $('admin-toggle').classList.toggle('primary', state.admin);
  }
  $('admin-toggle').addEventListener('click', function () {
    if (state.admin) {
      state.admin = false;
      state.code = null;
      $('admin-code').value = '';
      updateAdminUI();
      toast('已锁定');
      return;
    }
    $('admin-panel').classList.remove('hidden');
    $('admin-code').focus();
  });
  $('code-form').addEventListener('submit', async function (ev) {
    ev.preventDefault();
    var code = $('admin-code').value.trim();
    if (!code) return;
    try {
      await api('/api/session', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: code }) });
      state.code = code;
      state.admin = true;
      $('admin-code').value = '';
      updateAdminUI();
      toast('管理模式已启用');
      loadList();
    } catch (err) {
      toast(err.message);
    }
  });
  $('lock-btn').addEventListener('click', function () {
    state.admin = false;
    state.code = null;
    updateAdminUI();
    loadList();
  });

  async function loadList() {
    var listEl = $('file-list');
    try {
      var data = await api('/api/list?path=' + encodeURIComponent(state.path));
      var parts = state.path.split('/').filter(Boolean);
      $('crumb-more').textContent = state.path === '/' ? '' : parts.join(' / ');
      renderUsage(data.usage);
      if (!data.entries.length) {
        listEl.innerHTML = '<div class="empty">此目录为空</div>';
        return;
      }
      listEl.innerHTML = '';
      data.entries.forEach(function (it) {
        var row = document.createElement('div');
        row.className = 'file-row';
        var isDir = it.type === 'dir';
        var nameHtml;
        if (isDir) {
          nameHtml = '<button class="file-dir" data-open="' + esc(fullRel(it.name)) + '">' + esc(it.name) + '</button>';
        } else {
          nameHtml = '<a href="/api/download?path=' + encodeURIComponent(fullRel(it.name)) + '" download>' + esc(it.name) + '</a>';
        }
        var actions = '';
        if (state.admin) {
          actions = '<div class="file-actions">' +
            '<button class="btn small" data-act="rename" data-path="' + esc(fullRel(it.name)) + '">重命名</button>' +
            '<button class="btn small" data-act="copy" data-path="' + esc(fullRel(it.name)) + '">复制</button>' +
            '<button class="btn small" data-act="move" data-path="' + esc(fullRel(it.name)) + '">移动</button>' +
            '<button class="btn small danger" data-act="delete" data-path="' + esc(fullRel(it.name)) + '">删除</button>' +
            '</div>';
        }
        row.innerHTML =
          '<span class="file-icon">' + (isDir ? '📂' : '📄') + '</span>' +
          '<span class="file-name">' + nameHtml + '</span>' +
          '<span class="file-size">' + (isDir ? '—' : fmtBytes(it.size)) + '</span>' +
          '<span class="file-time">' + fmtTime(it.mtime) + '</span>' +
          actions;
        listEl.appendChild(row);
      });
    } catch (err) {
      listEl.innerHTML = '<div class="empty">加载失败：' + esc(err.message) + '</div>';
    }
  }

  function renderUsage(used) {
    var cap = 20 * 1024 * 1024 * 1024;
    var pct = Math.min(100, Math.round(used / cap * 100));
    $('usage-text').textContent = '使用 ' + fmtBytes(used) + ' / 20 GB';
    $('usage-pct').textContent = pct + '%';
    $('usage-fill').style.width = pct + '%';
  }

  async function openPath(rel) {
    state.path = rel;
    history.replaceState(null, '', '?path=' + encodeURIComponent(rel));
    loadList();
  }
  $('file-list').addEventListener('click', function (ev) {
    var btn = ev.target.closest('[data-open]');
    if (btn) {
      openPath(btn.getAttribute('data-open'));
      return;
    }
    var act = ev.target.closest('[data-act]');
    if (!act || !state.admin) return;
    var rel = act.getAttribute('data-path');
    var kind = act.getAttribute('data-act');
    if (kind === 'delete') {
      if (!confirm('确认删除 ' + rel + ' ？此操作不可恢复')) return;
      api('/api/delete', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: rel }) })
        .then(function () { toast('已删除'); loadList(); })
        .catch(function (e) { toast(e.message); });
    } else if (kind === 'rename') {
      var name = rel.split('/').pop();
      var newName = prompt('输入新名称', name);
      if (!newName || newName === name) return;
      api('/api/rename', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: rel, newName: newName.trim() }) })
        .then(function () { toast('已重命名'); loadList(); })
        .catch(function (e) { toast(e.message); });
    } else {
      var dest = prompt('输入目标文件夹（从根目录开始，例如 /documents，留空为根目录）', '/');
      if (dest == null) return;
      dest = String(dest).trim() || '/';
      api('/api/' + kind, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: rel, destDir: dest }) })
        .then(function () { toast(kind === 'copy' ? '已复制' : '已移动'); loadList(); })
        .catch(function (e) { toast(e.message); });
    }
  });

  $('root-btn').addEventListener('click', function () {
    state.path = '/';
    history.replaceState(null, '', location.pathname);
    loadList();
  });
  $('refresh-btn').addEventListener('click', loadList);
  $('mkdir-btn').addEventListener('click', function () {
    var name = prompt('输入文件夹名称');
    if (!name) return;
    api('/api/mkdir', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: state.path, name: name.trim() }) })
      .then(function () { toast('已创建'); loadList(); })
      .catch(function (e) { toast(e.message); });
  });
  $('upload-btn').addEventListener('click', function () { $('file-input').click(); });
  $('file-input').addEventListener('change', async function () {
    var files = Array.prototype.slice.call($('file-input').files || []);
    if (!files.length) return;
    for (var i = 0; i < files.length; i++) {
      var f = files[i];
      try {
        await api('/api/upload?path=' + encodeURIComponent(state.path), {
          method: 'POST',
          headers: { 'X-File-Name': encodeURIComponent(f.name) },
          body: f,
        });
        toast('已上传：' + f.name);
      } catch (e) {
        toast('上传失败 ' + f.name + '：' + e.message);
        break;
      }
    }
    $('file-input').value = '';
    loadList();
  });

  updateAdminUI();
  loadList();
  api('/api/usage').then(function (d) { renderUsage(d.used); }).catch(function () {});
})();
