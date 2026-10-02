# file-server

一个简单的文件站点：全站需要统一登录。只读会话可浏览、下载；管理会话还可上传、删除、
重命名、移动、复制。登录状态保持 7 天。

## 特性

- 两级会话：未登录看不到文件列表，只读会话不能修改文件
- 登录一次管 7 天：状态放在签名 Cookie 里，浏览器点下载链接也带得动
- 路径限制在根目录内，`..`、绝对路径等越界访问一律拒绝
- 配额上限，上传与复制前检查
- **单二进制 Go 服务**，编译出来直接跑，目标机器不需要装运行时

## 安全约束

| 约束 | 说明 |
| --- | --- |
| 根目录 | 只允许访问 `FILE_ROOT` 及其子目录，越界路径直接拒绝 |
| 配额 | 默认 20GB（`FILE_MAX_BYTES`），上传/复制前校验 |
| 登录码 | 由独立的 `auth-app` 管理，文件站只读取 `/etc/auth-session.env` 的签名密钥 |
| 会话 | 统一登录签发的 Cookie（HMAC），7 天；轮换签名密钥立即撤销旧会话 |

## 鉴权

`/api/session`（查状态）与 `/api/lock`（退出）免鉴权；`/api/unlock` 已停用并返回 410。其余接口未登录返回 401，只读会话访问修改接口返回 403。

- 登录入口：`https://tykrem.top/auth/`，签发跨站会话
- 浏览器下载直接带同一个 Cookie；文件站在每次请求验证只读或管理角色
- 主动退出：`POST /api/lock` 清除共享 Cookie

> 这个服务没有多用户体系；只读和管理是两种共享口令。管理码持有者能读写全站。
> 文件内容本身存在 `FILE_ROOT`，只由 nginx 反代 `/api/`，静态目录不直接暴露文件树。

## 快速开始

```bash
git clone git@github.com:TyKrem/file-server.git
cd file-server

mkdir -p /opt/file-server/root /opt/file-server/data
cp -a public/. /opt/file-server/public/

# 只用到标准库 + golang.org/x/text（中文排序），编译成单个可执行文件
go build -ldflags="-s -w" -o /opt/file-server/file-server ./server

cat > /etc/file-server.env <<'EOF'
FILE_PORT=8801
FILE_HOST=127.0.0.1
FILE_ROOT=/opt/file-server/root
FILE_DATA_DIR=/opt/file-server/data
EOF
chmod 600 /etc/file-server.env
# 另需 /etc/auth-session.env，见 auth-app/README.md
```

systemd 单元参考 `deploy/file-server.service`，站点配置参考
`deploy/nginx.http.conf`（签证书用）与 `deploy/nginx.https.conf`（HTTPS）。

nginx 负责托管 `public/` 下的静态页，只有 `/api/` 反代给这个服务。

## 实现说明

### 为什么用 Go

这类服务天生适合 Go：单个静态二进制、目标机器不用装运行时、并发 IO 不用自己管。
原来那版是 Node，目录占用统计要 `execFile('du', ...)` 甩给系统命令；现在自己走
目录，不需要 fork 进程。

### 与 Node 版的差异

重写时对着原实现做了逐请求比对（两份实例、相同的测试数据、同一串请求，比响应也
比落盘结果），下面两处是刻意保留的差异：

| 项 | Node 版 | Go 版 | 说明 |
| --- | --- | --- | --- |
| 用量统计 | `du -sb`（含目录 inode 大小）| 累加普通文件字节 | 少了 fork，读数更接近实际文件体积 |
| 中文排序 | `localeCompare(x, 'zh-CN')`（ICU）| x/text + 自建分档 | ICU 会给中文做脚本重排（汉字排拉丁前），x/text 的 `Reorder` 尚未实现，所以自己分了「符号→数字→汉字→拉丁→其他」 |

另外原版为了绕开 Node 的 URL 解析把百分号编码按 latin1 处理的问题，做了一次
`latin1 → UTF-8` 的补救；Go 的 URL 解析本身就按字节处理，这段补丁不再需要。

## 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/session` | 查会话状态：`{configured, unlocked}`（免鉴权）|
| `POST` | `/api/unlock` | 旧入口，返回 410 |
| `POST` | `/api/lock` | 清掉 Cookie（免鉴权）|
| `GET` | `/api/list?path=` | 列目录 |
| `GET` | `/api/download?path=` | 下载文件 |
| `GET` | `/api/usage` | 已用空间与配额 |
| `POST` | `/api/upload` | 上传 |
| `POST` | `/api/delete` | 删除 |
| `POST` | `/api/rename` | 重命名 |
| `POST` | `/api/move` | 移动 |
| `POST` | `/api/copy` | 复制 |
| `POST` | `/api/mkdir` | 新建目录 |

除前三个外，其余接口都要带统一登录签发的 Cookie；命令行验证：

```bash
curl -b 'tykrem_session=<从浏览器会话取出的令牌>' 'http://127.0.0.1:8801/api/list?path=/'
```

## 配置项

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `FILE_PORT` | `8801` | 监听端口 |
| `FILE_HOST` | `127.0.0.1` | 监听地址 |
| `FILE_ROOT` | `/opt/file-server/root` | 文件根目录 |
| `FILE_DATA_DIR` | `/opt/file-server/data` | 元数据目录 |
| `FILE_MAX_BYTES` | `21474836480` | 配额上限（字节） |
| `AUTH_SESSION_SECRET` | — | 统一会话签名密钥，必填；由 systemd 从 `/etc/auth-session.env` 注入 |

## License

[MIT](LICENSE)
