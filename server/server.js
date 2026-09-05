#!/usr/bin/env node
'use strict';

const http = require('http');
const fs = require('fs');
const fsp = fs.promises;
const path = require('path');
const crypto = require('crypto');
const { execFile } = require('child_process');
const { URL } = require('url');

const ENV = process.env;
const PORT = parseInt(ENV.FILE_PORT || '8801', 10);
const HOST = ENV.FILE_HOST || '127.0.0.1';
const ROOT = path.resolve(ENV.FILE_ROOT || '/opt/file-server/root');
const CAP_BYTES = Math.max(1, parseInt(ENV.FILE_MAX_BYTES || String(20 * 1024 * 1024 * 1024), 10) || 20 * 1024 * 1024 * 1024);
const DATA_DIR = ENV.FILE_DATA_DIR || '/opt/file-server/data';
let FALLBACK_CODE = '';
try {
  for (const line of fs.readFileSync('/etc/codex-chat.env', 'utf8').split('\n')) {
    const m = /^CHAT_SUPER_CODE=(.*)$/.exec(line.trim());
    if (m) FALLBACK_CODE = m[1].trim();
  }
} catch (e) {}
const ADMIN_CODE = String(ENV.FILE_ADMIN_CODE || FALLBACK_CODE).trim();

try { fs.mkdirSync(ROOT, { recursive: true }); } catch (e) {}
try { fs.mkdirSync(DATA_DIR, { recursive: true, mode: 0o700 }); } catch (e) {}

function safeEqual(a, b) {
  const ha = crypto.createHash('sha256').update(String(a)).digest();
  const hb = crypto.createHash('sha256').update(String(b)).digest();
  return crypto.timingSafeEqual(ha, hb);
}

function isAdmin(req) {
  if (!ADMIN_CODE) return false;
  const header = String(req.headers['x-admin-code'] || '').trim();
  return !!header && safeEqual(header, ADMIN_CODE);
}

function sendJson(res, status, obj) {
  const body = JSON.stringify(obj);
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store',
    'Content-Length': Buffer.byteLength(body),
  });
  res.end(body);
}

function readJson(req) {
  return new Promise(function (resolve, reject) {
    let data = '';
    req.on('data', function (c) {
      data += c;
      if (data.length > 1024 * 1024) {
        req.destroy();
        reject(new Error('请求体过大'));
      }
    });
    req.on('end', function () {
      try {
        resolve(JSON.parse(data || '{}'));
      } catch (e) {
        reject(new Error('无效的 JSON'));
      }
    });
    req.on('error', function () { reject(new Error('读取请求失败')); });
  });
}

function decodePath(raw) {
  if (raw == null || raw === '') return { rel: '/', abs: ROOT };
  let s = String(raw);
  try { s = decodeURIComponent(s); } catch (e) {}
  s = s.replace(/\\/g, '/');
  if (!s.startsWith('/')) s = '/' + s;
  const norm = path.normalize(s);
  if (norm.indexOf('\0') >= 0) return null;
  const abs = path.resolve(path.join(ROOT, norm));
  if (abs !== ROOT && !abs.startsWith(ROOT + path.sep)) return null;
  return { rel: norm, abs: abs };
}

async function ensureInside(abs) {
  const real = await fsp.realpath(abs);
  const rootReal = await fsp.realpath(ROOT);
  if (real !== rootReal && !real.startsWith(rootReal + path.sep)) {
    const err = new Error('路径越界');
    err.status = 403;
    throw err;
  }
  return real;
}

async function parentInside(abs) {
  return ensureInside(path.dirname(abs));
}

let usageCache = { ts: 0, bytes: 0, pending: null };
function getUsage(force) {
  const now = Date.now();
  if (!force && now - usageCache.ts < 5000 && usageCache.bytes >= 0) {
    return Promise.resolve(usageCache.bytes);
  }
  if (usageCache.pending) return usageCache.pending;
  usageCache.pending = new Promise(function (resolve) {
    execFile('du', ['-sb', ROOT], { timeout: 20000 }, function (err, stdout) {
      let bytes = 0;
      if (!err) {
        const m = /^(\d+)/.exec(String(stdout));
        if (m) bytes = Number(m[1]) || 0;
      }
      usageCache = { ts: Date.now(), bytes: bytes, pending: null };
      resolve(bytes);
    });
  });
  return usageCache.pending;
}

async function listDir(abs) {
  const st = await fsp.stat(abs);
  if (!st.isDirectory()) {
    const err = new Error('不是目录');
    err.status = 400;
    throw err;
  }
  const names = await fsp.readdir(abs, { withFileTypes: true });
  const entries = [];
  for (const d of names) {
    if (d.name === '.' || d.name === '..') continue;
    let st2 = null;
    try { st2 = await fsp.stat(path.join(abs, d.name)); } catch (e) { continue; }
    entries.push({
      name: d.name,
      type: st2.isDirectory() ? 'dir' : 'file',
      size: st2.isDirectory() ? null : st2.size,
      mtime: st2.mtimeMs,
    });
  }
  entries.sort(function (a, b) {
    if (a.type !== b.type) return a.type === 'dir' ? -1 : 1;
    return a.name.localeCompare(b.name, 'zh-CN');
  });
  return entries;
}

function contentType(name) {
  const ext = path.extname(name).toLowerCase();
  const map = {
    '.txt': 'text/plain; charset=utf-8', '.md': 'text/plain; charset=utf-8',
    '.json': 'application/json; charset=utf-8', '.js': 'text/javascript; charset=utf-8',
    '.css': 'text/css; charset=utf-8', '.html': 'text/html; charset=utf-8',
    '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.gif': 'image/gif',
    '.webp': 'image/webp', '.svg': 'image/svg+xml', '.pdf': 'application/pdf',
    '.zip': 'application/zip', '.gz': 'application/gzip', '.tar': 'application/x-tar',
  };
  return map[ext] || 'application/octet-stream';
}

function sendError(res, err) {
  const status = err && err.status ? err.status : 500;
  const message = err && err.message ? err.message : '服务器错误';
  if (status >= 500) console.error(message, err && err.stack || '');
  sendJson(res, status, { error: message });
}

async function handleDownload(req, res, u) {
  const p = decodePath(u.searchParams.get('path') || '/');
  if (!p) return sendJson(res, 400, { error: '路径无效' });
  let abs = p.abs;
  const st = await fsp.stat(abs);
  if (st.isDirectory()) return sendJson(res, 400, { error: '不能下载目录' });
  await ensureInside(abs);
  const name = path.basename(abs);
  const enc = encodeURIComponent(name).replace(/['()]/g, function (c) { return '%' + c.charCodeAt(0).toString(16).toUpperCase(); });
  res.writeHead(200, {
    'Content-Type': contentType(name),
    'Content-Length': st.size,
    'Content-Disposition': "attachment; filename*=UTF-8''" + enc,
    'Cache-Control': 'private, max-age=60',
  });
  const rs = fs.createReadStream(abs);
  rs.on('error', function () { try { res.destroy(); } catch (e) {} });
  rs.pipe(res);
}

function streamUpload(req, dest, maxBytes) {
  return new Promise(function (resolve, reject) {
    const ws = fs.createWriteStream(dest, { flags: 'wx', mode: 0o600 });
    let size = 0;
    let done = false;
    function fail(err) {
      if (done) return;
      done = true;
      req.removeAllListeners('data');
      try { ws.destroy(); } catch (e) {}
      try { fs.unlinkSync(dest); } catch (e) {}
      reject(err);
    }
    req.on('data', function (c) {
      if (done) return;
      size += c.length;
      if (size > maxBytes) {
        req.destroy();
        fail(Object.assign(new Error('超过 20GB 磁盘配额'), { status: 413 }));
        return;
      }
      if (!ws.write(c)) req.pause();
    });
    ws.on('drain', function () { if (!done) req.resume(); });
    ws.on('error', function (err) { fail(err); });
    ws.on('finish', function () {
      if (done) return;
      done = true;
      resolve(size);
    });
    req.on('error', function (err) { fail(err); });
    req.on('end', function () { if (!done) ws.end(); });
  });
}

async function handleApi(req, res, u) {
  const p = u.pathname;
  try {
    if (req.method === 'GET' && p === '/api/list') {
      const pathInfo = decodePath(u.searchParams.get('path') || '/');
      if (!pathInfo) return sendJson(res, 400, { error: '路径无效' });
      await ensureInside(pathInfo.abs);
      const entries = await listDir(pathInfo.abs);
      return sendJson(res, 200, { path: pathInfo.rel, entries: entries, usage: await getUsage(false) });
    }
    if (req.method === 'GET' && p === '/api/usage') {
      return sendJson(res, 200, { used: await getUsage(false), cap: CAP_BYTES });
    }
    if (req.method === 'GET' && p === '/api/download') {
      return handleDownload(req, res, u);
    }
    if (req.method === 'POST' && p === '/api/session') {
      const body = await readJson(req);
      if (!ADMIN_CODE || !body.code || !safeEqual(String(body.code || ''), ADMIN_CODE)) {
        return sendJson(res, 401, { error: '超级码不正确' });
      }
      return sendJson(res, 200, { ok: true });
    }
    if (!isAdmin(req)) {
      return sendJson(res, 401, { error: '需要超级码' });
    }
    if (req.method === 'POST' && p === '/api/mkdir') {
      const body = await readJson(req);
      const pathInfo = decodePath(body.path || '/');
      const name = String(body.name || '').replace(/[\\/]/g, '').trim();
      if (!pathInfo || !name || name === '.' || name === '..') return sendJson(res, 400, { error: '参数无效' });
      await parentInside(pathInfo.abs);
      const dest = path.join(pathInfo.abs, name);
      await fsp.mkdir(dest);
      usageCache.ts = 0;
      return sendJson(res, 200, { ok: true });
    }
    if (req.method === 'POST' && p === '/api/upload') {
      const pathInfo = decodePath(u.searchParams.get('path') || '/');
      let name = String(req.headers['x-file-name'] || '').trim();
      try { name = decodeURIComponent(name); } catch (e) {}
      name = path.basename(name.replace(/\\/g, '/'));
      if (!pathInfo || !name || name === '.' || name === '..') return sendJson(res, 400, { error: '参数无效' });
      await ensureInside(pathInfo.abs);
      const dirStat = await fsp.stat(pathInfo.abs);
      if (!dirStat.isDirectory()) return sendJson(res, 400, { error: '上传目录无效' });
      const dest = path.join(pathInfo.abs, name);
      try {
        await fsp.stat(dest);
        return sendJson(res, 409, { error: '同名文件已存在' });
      } catch (e) {}
      const used = await getUsage(true);
      const available = Math.max(0, CAP_BYTES - used);
      if (Number(req.headers['content-length']) > available) {
        return sendJson(res, 413, { error: '超过 20GB 磁盘配额' });
      }
      try {
        await streamUpload(req, dest, available);
      } catch (e) {
        usageCache.ts = 0;
        throw e;
      }
      usageCache.ts = 0;
      return sendJson(res, 200, { ok: true, size: (await fsp.stat(dest)).size });
    }
    if (req.method === 'POST' && p === '/api/delete') {
      const body = await readJson(req);
      const pathInfo = decodePath(body.path || '/');
      if (!pathInfo || pathInfo.abs === ROOT) return sendJson(res, 400, { error: '不能删除根目录' });
      await ensureInside(pathInfo.abs);
      await fsp.rm(pathInfo.abs, { recursive: true, force: false });
      usageCache.ts = 0;
      return sendJson(res, 200, { ok: true });
    }
    if (req.method === 'POST' && p === '/api/rename') {
      const body = await readJson(req);
      const pathInfo = decodePath(body.path || '');
      const newName = String(body.newName || '').replace(/[\\/]/g, '').trim();
      if (!pathInfo || !newName || pathInfo.abs === ROOT || newName === '.' || newName === '..') {
        return sendJson(res, 400, { error: '参数无效' });
      }
      await ensureInside(pathInfo.abs);
      const dest = path.join(path.dirname(pathInfo.abs), newName);
      await fsp.rename(pathInfo.abs, dest);
      return sendJson(res, 200, { ok: true });
    }
    if (req.method === 'POST' && (p === '/api/move' || p === '/api/copy')) {
      const body = await readJson(req);
      const src = decodePath(body.path || '');
      const dstDir = decodePath(body.destDir || '/');
      if (!src || !dstDir || src.abs === ROOT) return sendJson(res, 400, { error: '参数无效' });
      await ensureInside(src.abs);
      await ensureInside(dstDir.abs);
      const dstDirStat = await fsp.stat(dstDir.abs);
      if (!dstDirStat.isDirectory()) return sendJson(res, 400, { error: '目标必须是目录' });
      const dest = path.join(dstDir.abs, path.basename(src.abs));
      if (dest === src.abs || (p === '/api/move' && dest.startsWith(src.abs + path.sep))) {
        return sendJson(res, 400, { error: '不能移动到自身内部' });
      }
      try {
        await fsp.stat(dest);
        return sendJson(res, 409, { error: '目标位置已存在同名文件' });
      } catch (e) {}
      if (p === '/api/move') {
        await fsp.rename(src.abs, dest);
      } else {
        const used = await getUsage(true);
        const srcSize = await new Promise(function (resolve) {
          execFile('du', ['-sb', src.abs], { timeout: 20000 }, function (err, stdout) {
            const m = /^(\d+)/.exec(String(stdout));
            resolve(!err && m ? Number(m[1]) || 0 : 0);
          });
        });
        if (used + srcSize > CAP_BYTES) return sendJson(res, 413, { error: '复制后超过 20GB 磁盘配额' });
        const st = await fsp.stat(src.abs);
        if (st.isDirectory()) {
          await fsp.mkdir(dest);
          await copyTree(src.abs, dest);
        } else {
          await fsp.copyFile(src.abs, dest);
        }
      }
      usageCache.ts = 0;
      return sendJson(res, 200, { ok: true });
    }
    return sendJson(res, 404, { error: '接口不存在' });
  } catch (err) {
    sendError(res, err);
  }
}

async function copyTree(src, dest) {
  const entries = await fsp.readdir(src, { withFileTypes: true });
  for (const d of entries) {
    const s = path.join(src, d.name);
    const t = path.join(dest, d.name);
    if (d.isDirectory()) {
      await fsp.mkdir(t);
      await copyTree(s, t);
    } else {
      await fsp.copyFile(s, t);
    }
  }
}

const server = http.createServer(function (req, res) {
  const u = new URL(req.url || '/', 'http://localhost');
  if (u.pathname.indexOf('/api/') === 0) {
    handleApi(req, res, u);
    return;
  }
  sendJson(res, 404, { error: '请通过 /api/ 访问' });
});

server.listen(PORT, HOST, function () {
  console.log('file server listening on http://' + HOST + ':' + PORT);
  console.log('root=' + ROOT + ' cap=' + CAP_BYTES + ' admin=' + !!ADMIN_CODE);
});
