# file-server

一个简单的文件站点：**整个站点都要超级码**。输入一次超级码后可以浏览、下载、
上传、删除、重命名、移动、复制，解锁状态保持 12 小时，中途不用反复输码。

## 特性

- 全站超级码：没有码连文件列表都看不到，浏览与写操作一视同仁
- 解锁一次管 12 小时：状态放在签名 Cookie 里，浏览器点下载链接也带得动
- 路径限制在根目录内，`..`、绝对路径等越界访问一律拒绝
- 配额上限，上传与复制前检查
- **单二进制 Go 服务**，编译出来直接跑，目标机器不需要装运行时

## 安全约束

| 约束 | 说明 |
| --- | --- |
| 根目录 | 只允许访问 `FILE_ROOT` 及其子目录，越界路径直接拒绝 |
| 配额 | 默认 20GB（`FILE_MAX_BYTES`），上传/复制前校验 |
| 超级码 | `FILE_ADMIN_CODE`，未设置时回退读 `/etc/super-code.env` 的 `SUPER_CODE` |
| 解锁状态 | 超级码换来的签名 Cookie（HMAC，密钥就是超级码本身，改码即全部失效），12 小时 |

## 鉴权

整站一个码：只有 `/api/session`（查状态）、`/api/unlock`（解锁）、`/api/lock`（主动锁定）
是免鉴权的，其余接口未解锁一律 401。

- 解锁：`POST /api/unlock`，body `{"code":"超级码"}`，成功后下发 12 小时有效的 Cookie
- 之后浏览器怎么操作都不用再带码：下载是直接点链接，靠同一个 Cookie 放行
- 主动锁定：`POST /api/lock`，会清掉 Cookie，页面回到解锁页
- 换超级码 = 让所有已下发的 Cookie 立即失效（签名密钥就是码）

> 这个服务**没有**多用户与权限体系，"超级码"就是一个共享口令：拿到码的人能读写全站。
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
FILE_ADMIN_CODE=换成你的管理码
EOF
chmod 600 /etc/file-server.env
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
| `POST` | `/api/unlock` | 用超级码换 Cookie（免鉴权）|
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

除前三个外，其余接口都要带解锁后的 Cookie；命令行验证：

```bash
curl -c /tmp/fs.jar -X POST -H 'Content-Type: application/json' \
  -d '{"code":"<超级码>"}' http://127.0.0.1:8801/api/unlock
curl -b /tmp/fs.jar 'http://127.0.0.1:8801/api/list?path=/'
```

## 配置项

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `FILE_PORT` | `8801` | 监听端口 |
| `FILE_HOST` | `127.0.0.1` | 监听地址 |
| `FILE_ROOT` | `/opt/file-server/root` | 文件根目录 |
| `FILE_DATA_DIR` | `/opt/file-server/data` | 元数据目录 |
| `FILE_MAX_BYTES` | `21474836480` | 配额上限（字节） |
| `FILE_ADMIN_CODE` | — | 全站超级码；不设置时回退读 `/etc/super-code.env` 的 `SUPER_CODE` |

## License

[MIT](LICENSE)
