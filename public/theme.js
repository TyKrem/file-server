'use strict';

(function () {
  if (window.SiteTheme) return;
  var root = document.documentElement;
  var key = 'tykrem_theme';
  var modes = ['system', 'light', 'dark'];
  var names = { system: '跟随系统', light: '浅色', dark: '深色' };
  var media = window.matchMedia('(prefers-color-scheme: dark)');
  var button;
  var mode;
  function valid(value) { return modes.indexOf(value) >= 0; }
  function read() {
    var cookies = '';
    try { cookies = document.cookie; } catch (error) {}
    var entry = cookies.split(';').map(function (value) { return value.trim(); })
      .find(function (value) { return value.indexOf(key + '=') === 0; });
    if (entry && valid(entry.slice(key.length + 1))) return entry.slice(key.length + 1);
    try { var saved = localStorage.getItem(key); if (valid(saved)) return saved; } catch (error) {}
    return 'system';
  }
  function apply(next) {
    mode = valid(next) ? next : 'system';
    var theme = mode === 'system' ? (media.matches ? 'dark' : 'light') : mode;
    var changed = root.dataset.theme !== theme;
    root.dataset.theme = theme;
    root.dataset.themeMode = mode;
    if (button) {
      button.querySelector('.site-theme-label').textContent = names[mode];
      button.querySelector('.site-theme-icon').textContent = mode === 'system' ? '◐' : theme === 'dark' ? '☾' : '☀';
      button.setAttribute('aria-label', '主题：' + names[mode] + '，点击切换为' + names[modes[(modes.indexOf(mode) + 1) % modes.length]]);
      button.title = button.getAttribute('aria-label');
    }
    if (changed) window.dispatchEvent(new CustomEvent('site-theme-change', { detail: { mode: mode, theme: theme } }));
  }
  function choose(next) {
    if (!valid(next)) return;
    // 主题偏好不含身份信息；父域 Cookie 让独立子站共用选择。
    var domain = location.hostname === 'tykrem.top' || location.hostname.endsWith('.tykrem.top') ? '; Domain=tykrem.top' : '';
    try { document.cookie = key + '=' + next + '; Path=/; Max-Age=31536000; SameSite=Lax' + domain + (location.protocol === 'https:' ? '; Secure' : ''); } catch (error) {}
    try { localStorage.setItem(key, next); } catch (error) {}
    apply(next);
  }
  function mount() {
    if (document.querySelector('.site-theme-switch')) return;
    button = document.createElement('button');
    button.type = 'button'; button.className = 'site-theme-switch';
    button.innerHTML = '<span class="site-theme-icon" aria-hidden="true"></span><span class="site-theme-label"></span>';
    button.addEventListener('click', function () { choose(modes[(modes.indexOf(mode) + 1) % modes.length]); });
    var target = document.querySelector('[data-theme-control], .speed-links, .top-right, .appbar-actions, .topbar-actions, .topbar .sites');
    if (target) target.appendChild(button);
    else { button.classList.add('site-theme-floating'); document.body.appendChild(button); }
    apply(read());
  }
  apply(read());
  window.SiteTheme = { choose: choose };
  media.addEventListener('change', function () { if (mode === 'system') apply('system'); });
  window.addEventListener('storage', function (event) { if (event.key === key || event.key === null) apply(read()); });
  window.addEventListener('pageshow', function () { apply(read()); });
  window.addEventListener('focus', function () { apply(read()); });
  document.addEventListener('visibilitychange', function () { if (!document.hidden) apply(read()); });
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', mount);
  else mount();
})();
