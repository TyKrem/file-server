# 文件服务（file.tykrem.top）

公开文件浏览/下载；游客只读。输入超级码后可上传、删除、重命名、移动、复制。

## 安全约束

- 只允许访问 `/opt/file-server/root` 及其子目录，越界路径一律拒绝
- 文件配额上限 20GB（上传与复制前检查）
- 管理模式使用超级码（默认读 `/etc/codex-chat.env` 的 `CHAT_SUPER_CODE`）

## 目录

| 路径 | 说明 |
| --- | --- |
| `/root/file-server` | 源码（git） |
| `/opt/file-server` | 运行目录 |
| `/opt/file-server/root` | 文件根目录（只放允许公开的文件） |
| `/etc/systemd/system/file-server.service` | 后端服务（127.0.0.1:8801） |
| `/etc/nginx/conf.d/file.tykrem.top.conf` | 站点配置 |

## 同步部署

```bash
cp -a /root/file-server/public/. /opt/file-server/public/
cp /root/file-server/server/server.js /opt/file-server/server/server.js
systemctl restart file-server
nginx -t && systemctl reload nginx
```
