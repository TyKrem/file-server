'use strict';

(function () {
  function isImage(name) { return /\.(png|jpe?g|webp|gif|bmp|ico)$/i.test(name); }
  function create(options) {
    var element = function (id) { return document.getElementById(id); };
    var dialog = element('image-dialog'); var stage = element('image-stage'); var image = element('image-content');
    var gallery = []; var current = ''; var zoom = 1; var horizontal = 0; var vertical = 0;
    var fittedWidth = 0; var fittedHeight = 0; var dark = false; var request = 0; var controller = null; var objectUrl = null;
    var returnFocus = null; var pointers = new Map(); var gesture = null; var copyTimer = null;
    function transform() {
      horizontal = Math.max(-Math.max(0, (fittedWidth * zoom - stage.clientWidth) / 2), Math.min(Math.max(0, (fittedWidth * zoom - stage.clientWidth) / 2), horizontal));
      vertical = Math.max(-Math.max(0, (fittedHeight * zoom - stage.clientHeight) / 2), Math.min(Math.max(0, (fittedHeight * zoom - stage.clientHeight) / 2), vertical));
      image.style.transform = 'translate(-50%,-50%) translate(' + horizontal + 'px,' + vertical + 'px) scale(' + zoom + ')';
      element('image-fit').textContent = zoom === 1 ? '适应' : Math.round(zoom * 100) + '%';
      element('image-zoom-out').disabled = image.hidden || zoom <= 1;
      element('image-zoom-in').disabled = image.hidden || zoom >= 8;
      stage.dataset.zoomed = String(zoom > 1);
    }
    function fit() {
      if (!image.naturalWidth || image.hidden) return;
      var ratio = Math.min(stage.clientWidth / image.naturalWidth, stage.clientHeight / image.naturalHeight, 1);
      fittedWidth = image.naturalWidth * ratio; fittedHeight = image.naturalHeight * ratio;
      image.style.width = fittedWidth + 'px'; image.style.height = fittedHeight + 'px'; transform();
    }
    function setZoom(value) { zoom = Math.max(1, Math.min(8, value)); if (zoom === 1) { horizontal = 0; vertical = 0; } transform(); }
    function clearImage() {
      request++; if (controller) controller.abort(); controller = null;
      image.onload = null; image.onerror = null; image.removeAttribute('src'); image.hidden = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl); objectUrl = null; pointers.clear(); gesture = null;
    }
    async function load(rel) {
      clearImage(); var number = request; controller = new AbortController(); current = rel; zoom = 1; horizontal = 0; vertical = 0;
      element('image-title').textContent = rel.split('/').pop(); image.alt = rel.split('/').pop();
      var index = gallery.indexOf(rel);
      element('image-counter').textContent = (index + 1) + ' / ' + gallery.length;
      element('image-previous').disabled = index <= 0; element('image-next').disabled = index >= gallery.length - 1;
      element('image-download').href = '/api/download?path=' + encodeURIComponent(rel); element('image-download').download = rel.split('/').pop();
      element('image-status').hidden = false; element('image-status').textContent = '正在读取图片…'; element('image-size').textContent = '';
      stage.setAttribute('aria-busy', 'true'); transform();
      try {
        var response = await fetch('/api/preview?path=' + encodeURIComponent(rel), { signal: controller.signal });
        if (!response.ok) {
          var failure = await response.json().catch(function () { return {}; });
          if (number !== request) return;
          if (response.status === 401) { options.onUnauthorized(); return; }
          throw new Error(failure.error || 'HTTP ' + response.status);
        }
        var blob = await response.blob(); if (number !== request) return;
        objectUrl = URL.createObjectURL(blob);
        image.onload = function () {
          if (number !== request) return;
          image.hidden = false; element('image-status').hidden = true; stage.setAttribute('aria-busy', 'false');
          element('image-size').textContent = image.naturalWidth + ' × ' + image.naturalHeight + ' px'; fit();
        };
        image.onerror = function () {
          if (number !== request) return;
          image.hidden = true; stage.setAttribute('aria-busy', 'false'); element('image-status').hidden = false; element('image-status').textContent = '浏览器无法解码这张图片，可下载后查看。'; transform();
        };
        image.src = objectUrl;
      } catch (error) {
        if (number !== request || error.name === 'AbortError') return;
        element('image-status').textContent = '无法预览：' + error.message; stage.setAttribute('aria-busy', 'false');
      }
    }
    function open(rel, entries, notify) {
      if (!isImage(rel)) { options.toast('该文件不支持图片预览，请下载查看'); return; }
      gallery = entries.filter(isImage); if (!gallery.includes(rel)) gallery.push(rel);
      if (!dialog.open) { returnFocus = document.activeElement; dialog.showModal(); document.body.classList.add('preview-open'); }
      if (notify !== false) options.onChange(rel, true);
      load(rel);
    }
    function close(notify) {
      if (!dialog.open) return;
      dialog.close();
      clearImage(); document.body.classList.remove('preview-open');
      if (notify !== false) options.onClose();
      if (returnFocus && returnFocus.isConnected) returnFocus.focus({ preventScroll: true });
    }
    dialog.addEventListener('cancel', function (event) { event.preventDefault(); close(); });
    dialog.addEventListener('click', function (event) {
      var bounds = dialog.getBoundingClientRect();
      if (event.target === dialog && (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom)) close();
    });
    function next(offset) {
      var rel = gallery[gallery.indexOf(current) + offset]; if (!rel) return;
      options.onChange(rel, false); load(rel);
    }
    element('image-close').addEventListener('click', function () { close(); });
    element('image-previous').addEventListener('click', function () { next(-1); }); element('image-next').addEventListener('click', function () { next(1); });
    element('image-zoom-in').addEventListener('click', function () { setZoom(zoom * 1.5); }); element('image-zoom-out').addEventListener('click', function () { setZoom(zoom / 1.5); });
    element('image-fit').addEventListener('click', function () { setZoom(1); });
    element('image-background').addEventListener('click', function () {
      dark = !dark; stage.dataset.background = dark ? 'dark' : 'light';
      this.setAttribute('aria-pressed', String(dark)); this.textContent = dark ? '浅色底' : '深色底';
    });
    element('image-copy').addEventListener('click', async function () {
      var link = new URL(location.href); link.searchParams.set('image', current);
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) await navigator.clipboard.writeText(link.href);
        else {
          var input = document.createElement('textarea'); input.value = link.href; input.style.position = 'fixed'; input.style.opacity = '0'; dialog.appendChild(input);
          try { input.select(); if (!document.execCommand('copy')) throw new Error('浏览器不支持自动复制'); } finally { input.remove(); }
        }
        element('image-copy').textContent = '已复制';
      } catch (error) { element('image-copy').textContent = '复制失败'; element('image-copy').title = '可复制浏览器地址栏中的链接'; }
      clearTimeout(copyTimer); copyTimer = setTimeout(function () { element('image-copy').textContent = '复制链接'; }, 2500);
    });
    dialog.addEventListener('keydown', function (event) {
      if (event.altKey || event.ctrlKey || event.metaKey || event.target.closest('input,textarea,select')) return;
      if (event.key === 'ArrowLeft') { event.preventDefault(); next(-1); }
      else if (event.key === 'ArrowRight') { event.preventDefault(); next(1); }
      else if (['+', '='].includes(event.key)) { event.preventDefault(); setZoom(zoom * 1.5); }
      else if (event.key === '-') { event.preventDefault(); setZoom(zoom / 1.5); }
      else if (event.key === '0') { event.preventDefault(); setZoom(1); }
    });
    stage.addEventListener('dblclick', function () { if (!image.hidden) setZoom(zoom === 1 ? 2 : 1); });
    stage.addEventListener('wheel', function (event) { if (!image.hidden) { event.preventDefault(); setZoom(zoom * (event.deltaY < 0 ? 1.15 : 1 / 1.15)); } }, { passive: false });
    function beginGesture() {
      var positions = Array.from(pointers.values());
      gesture = positions.length === 2 ? { distance: Math.hypot(positions[0].x - positions[1].x, positions[0].y - positions[1].y), zoom: zoom } : positions.length ? { x: positions[0].x, y: positions[0].y, horizontal: horizontal, vertical: vertical } : null;
    }
    stage.addEventListener('pointerdown', function (event) {
      if (image.hidden || event.button !== 0) return;
      pointers.set(event.pointerId, { x: event.clientX, y: event.clientY }); stage.setPointerCapture(event.pointerId); beginGesture();
    });
    stage.addEventListener('pointermove', function (event) {
      if (!pointers.has(event.pointerId) || !gesture) return;
      pointers.set(event.pointerId, { x: event.clientX, y: event.clientY });
      var positions = Array.from(pointers.values());
      if (positions.length === 2 && gesture.distance) setZoom(gesture.zoom * Math.hypot(positions[0].x - positions[1].x, positions[0].y - positions[1].y) / gesture.distance);
      else if (positions.length === 1 && gesture.x !== undefined && zoom > 1) { horizontal = gesture.horizontal + event.clientX - gesture.x; vertical = gesture.vertical + event.clientY - gesture.y; transform(); }
    });
    ['pointerup', 'pointercancel', 'lostpointercapture'].forEach(function (type) { stage.addEventListener(type, function (event) { pointers.delete(event.pointerId); beginGesture(); }); });
    new ResizeObserver(fit).observe(stage);
    return { open: open, close: close, isOpen: function () { return dialog.open; } };
  }
  window.FileImageViewer = { create: create, isImage: isImage };
})();
