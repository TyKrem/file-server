// file-server 的后端：只处理 /api/ 下的接口，静态页由 nginx 直接托管 public/。
//
// 实现要点跟原来的 Node 版一一对应，有几处刻意的差别写在注释里。
package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

const (
	defaultPort = 8801
	defaultCap  = int64(20) * 1024 * 1024 * 1024
	maxJSONBody = 1 << 20 // 1MB
	usageTTL    = 5 * time.Second
)

var (
	port      int
	host      string
	root      string // 已经 Abs 处理，末尾不带分隔符
	rootReal  string // realpath，用于挡符号链接越界
	capBytes  int64
	dataDir   string
	adminCode string
)

// ---------------------------------------------------------------------------
// 启动
// ---------------------------------------------------------------------------

func envInt(key string, def int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// loadFallbackCode 在没配 FILE_ADMIN_CODE 时，回退读 chat-app 的超级码。
// 保持跟 Node 版一致：读不到就当没配。
func loadFallbackCode() string {
	data, err := os.ReadFile("/etc/codex-chat.env")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "CHAT_SUPER_CODE="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func main() {
	port = int(envInt("FILE_PORT", defaultPort))
	host = os.Getenv("FILE_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	root = os.Getenv("FILE_ROOT")
	if root == "" {
		root = "/opt/file-server/root"
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	root = filepath.Clean(root)

	capBytes = envInt("FILE_MAX_BYTES", defaultCap)
	if capBytes < 1 {
		capBytes = 1
	}

	dataDir = os.Getenv("FILE_DATA_DIR")
	if dataDir == "" {
		dataDir = "/opt/file-server/data"
	}

	adminCode = strings.TrimSpace(os.Getenv("FILE_ADMIN_CODE"))
	if adminCode == "" {
		adminCode = loadFallbackCode()
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Printf("创建根目录失败 %s：%v", root, err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Printf("创建数据目录失败 %s：%v", dataDir, err)
	}
	// Node 版每次请求都 realpath(ROOT)，这里启动时解一次；根目录一般不会中途被换掉，
	// 真被换掉重启即可。
	if real, err := filepath.EvalSymlinks(root); err == nil {
		rootReal = real
	} else {
		rootReal = root
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/", handleAPI)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, http.StatusNotFound, map[string]any{"error": "请通过 /api/ 访问"})
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
		// 上传大文件可能跑很久，写超时不能设；读超时同理交给客户端的流控。
	}
	log.Printf("file server listening on http://%s:%d", host, port)
	log.Printf("root=%s cap=%d admin=%v", root, capBytes, adminCode != "")
	reportLog("info", "文件服务启动", map[string]any{
		"port":            port,
		"root":            root,
		"quotaBytes":      capBytes,
		"adminConfigured": adminCode != "",
	})
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("服务退出：%v", err)
	}
}

// ---------------------------------------------------------------------------
// 基础工具
// ---------------------------------------------------------------------------

// apiError 带 HTTP 状态码的业务错误，对应 Node 版给 err 挂 status 的写法。
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func errStatus(status int, msg string) error { return &apiError{status: status, msg: msg} }

func sendJSON(w http.ResponseWriter, status int, obj any) {
	body, err := json.Marshal(obj)
	if err != nil {
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// safeEqual 用 sha256 摘要长度对齐后做常数时间比较，避免按长度泄漏口令长度。
func safeEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

func isAdmin(r *http.Request) bool {
	if adminCode == "" {
		return false
	}
	got := strings.TrimSpace(r.Header.Get("X-Admin-Code"))
	return got != "" && safeEqual(got, adminCode)
}

func readJSON(r *http.Request) (map[string]any, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBody+1))
	if err != nil {
		return nil, errors.New("读取请求失败")
	}
	if len(body) > maxJSONBody {
		return nil, errors.New("请求体过大")
	}
	out := map[string]any{}
	if len(body) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, errors.New("无效的 JSON")
	}
	return out, nil
}

func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// ---------------------------------------------------------------------------
// 路径
// ---------------------------------------------------------------------------

type pathInfo struct {
	rel string
	abs string
}

// decodePath 把客户端传来的 path 参数解析成根目录内的绝对路径。
// 越界（.. / 绝对路径 / NUL）一律返回 nil。
//
// 跟 Node 版的差别：那边要额外做一次 latin1→UTF-8 的补救，因为 Node 的
// URL 解析把百分号编码的字节按 latin1 塞进了字符串，导致中文路径变乱码。
// Go 的 url 解析本身就是按字节处理的，百分号编码和裸 UTF-8 都能正确还原，
// 所以这个补丁不需要了（两种发法都实测过）。
func decodePath(raw string) *pathInfo {
	if raw == "" {
		return &pathInfo{rel: "/", abs: root}
	}
	s := strings.ReplaceAll(raw, "\\", "/")
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	if strings.ContainsRune(s, 0) {
		return nil
	}
	clean := path.Clean(s)
	abs := filepath.Join(root, clean)
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return nil
	}
	return &pathInfo{rel: clean, abs: abs}
}

// ensureInside 用 realpath 判断目标是否仍在根目录内，挡掉指向外面的符号链接。
func ensureInside(abs string) (string, error) {
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
		return "", errStatus(http.StatusForbidden, "路径越界")
	}
	return real, nil
}

// ---------------------------------------------------------------------------
// 用量统计
// ---------------------------------------------------------------------------

var usageCache struct {
	sync.Mutex
	ts      time.Time
	bytes   int64
	ready   bool
	pending chan struct{}
}

// getUsage 统计根目录占用。
//
// 跟 Node 版的差别：那边是 `execFile('du', ['-sb', ROOT])`，这里自己走目录。
// 换来的是不用每次 fork 一个进程，代价是数字略有出入——du -sb 会把目录自身
// 的 inode 大小也算进去，这里只统计普通文件，所以读数会略小、也更接近「实际
// 存放的文件字节数」。
func getUsage(force bool) int64 {
	usageCache.Lock()
	if !force && usageCache.ready && time.Since(usageCache.ts) < usageTTL {
		n := usageCache.bytes
		usageCache.Unlock()
		return n
	}
	if usageCache.pending != nil {
		done := usageCache.pending
		usageCache.Unlock()
		<-done
		usageCache.Lock()
		n := usageCache.bytes
		usageCache.Unlock()
		return n
	}
	done := make(chan struct{})
	usageCache.pending = done
	usageCache.Unlock()

	var total int64
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 读不到就跳过，不因为一个坏条目让整次统计失败
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		total += info.Size()
		return nil
	})

	usageCache.Lock()
	usageCache.ts = time.Now()
	usageCache.bytes = total
	usageCache.ready = true
	usageCache.pending = nil
	usageCache.Unlock()
	close(done)
	return total
}

func invalidateUsage() {
	usageCache.Lock()
	usageCache.ready = false
	usageCache.Unlock()
}

// ---------------------------------------------------------------------------
// 目录列举
// ---------------------------------------------------------------------------

type entry struct {
	Name  string  `json:"name"`
	Type  string  `json:"type"`
	Size  *int64  `json:"size"`
	Mtime float64 `json:"mtime"`
}

// 响应用结构体而不是 map：Go 的 encoding/json 对 map 是按 key 排序输出的，
// 而 Node 的 JSON.stringify 保持插入顺序。字段顺序语义上无所谓，但保持跟原来
// 一致能少一处无谓差异（Content-Length 也会跟着对上）。
type listResponse struct {
	Path    string  `json:"path"`
	Entries []entry `json:"entries"`
	Usage   int64   `json:"usage"`
}

type usageResponse struct {
	Used int64 `json:"used"`
	Cap  int64 `json:"cap"`
}

// 目录优先，然后按中文习惯（拼音）排序。Node 版用 localeCompare(x, 'zh-CN')，
// Go 标准库没有本地化排序，所以引了 golang.org/x/text —— 不引的话中文会退化成
// 按 UTF-8 码点排，顺序跟改版前对不上。
var (
	collatorMu sync.Mutex
	collator   = collate.New(language.Chinese)
)

// sortRank 复刻 ICU 在 zh-CN 下的脚本重排：符号 → 数字 → 汉字 → 拉丁 → 其他文字。
//
// 为什么自己分档：x/text 的 collate.Reorder 在 v0.32.0 还是 TODO，一调用就
// panic("TODO: implement")，而 ICU 给中文做了这个重排（中文用户习惯看到中文排
// 前面）。不分档的话中文会排到英文后面，跟改版前肉眼可见地不一样。
//
// 这是近似实现，只按首个字符判断，够覆盖文件名场景；同档内仍交给 collator，
// 拼音顺序与 ICU 一致。
func sortRank(s string) int {
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			return 2
		case r >= '0' && r <= '9':
			return 1
		case r < unicode.MaxASCII && unicode.IsLetter(r):
			return 3
		case unicode.IsLetter(r):
			return 4
		default:
			return 0 // 符号、标点、空白
		}
	}
	return 0
}

func lessName(a, b string) bool {
	ra, rb := sortRank(a), sortRank(b)
	if ra != rb {
		return ra < rb
	}
	return collator.CompareString(a, b) < 0
}

func listDir(abs string) ([]entry, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errStatus(http.StatusBadRequest, "不是目录")
	}
	items, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	entries := make([]entry, 0, len(items))
	for _, d := range items {
		name := d.Name()
		if name == "." || name == ".." {
			continue
		}
		st, err := os.Stat(filepath.Join(abs, name))
		if err != nil {
			continue // 坏的符号链接之类的直接跳过，跟 Node 版一致
		}
		// 跟 Node 的 stats.mtimeMs 一样算：秒*1000 + 纳秒/1e6，保留小数毫秒
		mod := st.ModTime()
		e := entry{Name: name, Mtime: float64(mod.Unix())*1000 + float64(mod.Nanosecond())/1e6}
		if st.IsDir() {
			e.Type = "dir"
		} else {
			e.Type = "file"
			size := st.Size()
			e.Size = &size
		}
		entries = append(entries, e)
	}
	collatorMu.Lock()
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Type != b.Type {
			return a.Type == "dir"
		}
		return lessName(a.Name, b.Name)
	})
	collatorMu.Unlock()
	return entries, nil
}

var contentTypes = map[string]string{
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".pdf":  "application/pdf",
	".zip":  "application/zip",
	".gz":   "application/gzip",
	".tar":  "application/x-tar",
}

func contentType(name string) string {
	if ct, ok := contentTypes[strings.ToLower(filepath.Ext(name))]; ok {
		return ct
	}
	return "application/octet-stream"
}

func sendError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "服务器错误"
	var ae *apiError
	if errors.As(err, &ae) {
		status = ae.status
		message = ae.msg
	} else if err != nil {
		message = err.Error()
	}
	// 文件不存在（含真被删了、符号链接断了）按 404 回，不当作服务端故障
	if status == http.StatusInternalServerError && errors.Is(err, fs.ErrNotExist) {
		status = http.StatusNotFound
		message = "文件不存在"
	}
	if status >= 500 {
		log.Printf("请求处理失败：%s（%v）", message, err)
		reportLog("error", "请求处理失败", map[string]any{"status": status, "error": message})
	}
	sendJSON(w, status, map[string]any{"error": message})
}

// ---------------------------------------------------------------------------
// 接口
// ---------------------------------------------------------------------------

func handleAPI(w http.ResponseWriter, r *http.Request) {
	if err := route(w, r); err != nil {
		sendError(w, err)
	}
}

func route(w http.ResponseWriter, r *http.Request) error {
	// Go 1.22 起 ServeMux 直接把方法带进 pattern，但这里要跟原来一样对
	// 「路径对、方法不对」也回 404 而不是 405，所以自己分派。
	q := r.URL.Query()
	endpoint := r.URL.Path

	switch {
	case r.Method == http.MethodGet && endpoint == "/api/list":
		info := decodePath(q.Get("path"))
		if info == nil {
			return errStatus(http.StatusBadRequest, "路径无效")
		}
		if _, err := ensureInside(info.abs); err != nil {
			return err
		}
		entries, err := listDir(info.abs)
		if err != nil {
			return err
		}
		sendJSON(w, http.StatusOK, listResponse{Path: info.rel, Entries: entries, Usage: getUsage(false)})
		return nil

	case r.Method == http.MethodGet && endpoint == "/api/usage":
		sendJSON(w, http.StatusOK, usageResponse{Used: getUsage(false), Cap: capBytes})
		return nil

	case r.Method == http.MethodGet && endpoint == "/api/download":
		return handleDownload(w, r)

	case r.Method == http.MethodPost && endpoint == "/api/session":
		body, err := readJSON(r)
		if err != nil {
			return err
		}
		code := str(body, "code")
		if adminCode == "" || code == "" || !safeEqual(code, adminCode) {
			return errStatus(http.StatusUnauthorized, "超级码不正确")
		}
		sendJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}

	if !isAdmin(r) {
		return errStatus(http.StatusUnauthorized, "需要超级码")
	}

	switch {
	case r.Method == http.MethodPost && endpoint == "/api/mkdir":
		return handleMkdir(w, r)
	case r.Method == http.MethodPost && endpoint == "/api/upload":
		return handleUpload(w, r)
	case r.Method == http.MethodPost && endpoint == "/api/delete":
		return handleDelete(w, r)
	case r.Method == http.MethodPost && endpoint == "/api/rename":
		return handleRename(w, r)
	case r.Method == http.MethodPost && (endpoint == "/api/move" || endpoint == "/api/copy"):
		return handleMoveCopy(w, r, endpoint == "/api/move")
	}
	return errStatus(http.StatusNotFound, "接口不存在")
}

func handleDownload(w http.ResponseWriter, r *http.Request) error {
	info := decodePath(r.URL.Query().Get("path"))
	if info == nil {
		return errStatus(http.StatusBadRequest, "路径无效")
	}
	st, err := os.Stat(info.abs)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return errStatus(http.StatusBadRequest, "不能下载目录")
	}
	if _, err := ensureInside(info.abs); err != nil {
		return err
	}
	f, err := os.Open(info.abs)
	if err != nil {
		return err
	}
	defer f.Close()

	name := filepath.Base(info.abs)
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+rfc5987Encode(name))
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f) // 客户端断开时这里会报错，忽略即可
	return nil
}

// rfc5987Encode 按 Node 版的方式编码文件名：encodeURIComponent 之后再把
// ' ( ) 也转义（这三个是 encodeURIComponent 不转的）。
func rfc5987Encode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-' || c == '_' || c == '.' || c == '!' || c == '~' || c == '*':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func handleMkdir(w http.ResponseWriter, r *http.Request) error {
	body, err := readJSON(r)
	if err != nil {
		return err
	}
	info := decodePath(str(body, "path"))
	name := strings.TrimSpace(str(body, "name"))
	if info == nil {
		return errStatus(http.StatusBadRequest, "参数无效")
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return errStatus(http.StatusBadRequest, "文件夹名称不能包含路径分隔符，只需填写名称")
	}
	if _, err := ensureInside(info.abs); err != nil {
		return err
	}
	st, err := os.Stat(info.abs)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errStatus(http.StatusBadRequest, "当前位置不是文件夹")
	}
	if err := os.Mkdir(filepath.Join(info.abs, name), 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return errStatus(http.StatusConflict, "同名文件夹已存在")
		}
		return err
	}
	invalidateUsage()
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func handleUpload(w http.ResponseWriter, r *http.Request) error {
	info := decodePath(r.URL.Query().Get("path"))
	name := strings.TrimSpace(r.Header.Get("X-File-Name"))
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if info == nil || name == "" || name == "." || name == ".." || name == "/" {
		return errStatus(http.StatusBadRequest, "参数无效")
	}
	if _, err := ensureInside(info.abs); err != nil {
		return err
	}
	st, err := os.Stat(info.abs)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errStatus(http.StatusBadRequest, "上传目录无效")
	}
	dest := filepath.Join(info.abs, name)
	if _, err := os.Stat(dest); err == nil {
		return errStatus(http.StatusConflict, "同名文件已存在")
	}

	used := getUsage(true)
	available := capBytes - used
	if available < 0 {
		available = 0
	}
	if declared, err := strconv.ParseInt(r.Header.Get("Content-Length"), 10, 64); err == nil && declared > available {
		return errStatus(http.StatusRequestEntityTooLarge, "超过 20GB 磁盘配额")
	}

	size, err := streamUpload(r.Body, dest, available)
	if err != nil {
		invalidateUsage()
		return err
	}
	invalidateUsage()
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "size": size})
	return nil
}

// streamUpload 独占创建（O_EXCL）后边读边写，超配额就中断并删掉半截文件。
// 独占创建同时也挡住了并发上传同名文件互相覆盖。
func streamUpload(src io.Reader, dest string, maxBytes int64) (int64, error) {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return 0, errStatus(http.StatusConflict, "同名文件已存在")
		}
		return 0, err
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(dest)
	}
	// 多读一个字节，用来判断是不是真的超了
	written, err := io.Copy(f, io.LimitReader(src, maxBytes+1))
	if err != nil {
		cleanup()
		return 0, err
	}
	if written > maxBytes {
		cleanup()
		return 0, errStatus(http.StatusRequestEntityTooLarge, "超过 20GB 磁盘配额")
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dest)
		return 0, err
	}
	return written, nil
}

func handleDelete(w http.ResponseWriter, r *http.Request) error {
	body, err := readJSON(r)
	if err != nil {
		return err
	}
	info := decodePath(str(body, "path"))
	if info == nil || info.abs == root {
		return errStatus(http.StatusBadRequest, "不能删除根目录")
	}
	if _, err := ensureInside(info.abs); err != nil {
		return err
	}
	if err := os.RemoveAll(info.abs); err != nil {
		return err
	}
	invalidateUsage()
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func handleRename(w http.ResponseWriter, r *http.Request) error {
	body, err := readJSON(r)
	if err != nil {
		return err
	}
	info := decodePath(str(body, "path"))
	newName := strings.TrimSpace(strings.NewReplacer("\\", "", "/", "").Replace(str(body, "newName")))
	if info == nil || newName == "" || info.abs == root || newName == "." || newName == ".." {
		return errStatus(http.StatusBadRequest, "参数无效")
	}
	if _, err := ensureInside(info.abs); err != nil {
		return err
	}
	dest := filepath.Join(filepath.Dir(info.abs), newName)
	if _, err := os.Stat(dest); err == nil {
		return errStatus(http.StatusConflict, "目标名称已存在")
	}
	if err := os.Rename(info.abs, dest); err != nil {
		return err
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func handleMoveCopy(w http.ResponseWriter, r *http.Request, isMove bool) error {
	body, err := readJSON(r)
	if err != nil {
		return err
	}
	src := decodePath(str(body, "path"))
	dstDir := decodePath(str(body, "destDir"))
	if src == nil || dstDir == nil || src.abs == root {
		return errStatus(http.StatusBadRequest, "参数无效")
	}
	if _, err := ensureInside(src.abs); err != nil {
		return err
	}
	if _, err := ensureInside(dstDir.abs); err != nil {
		return err
	}
	st, err := os.Stat(dstDir.abs)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errStatus(http.StatusBadRequest, "目标必须是目录")
	}
	dest := filepath.Join(dstDir.abs, filepath.Base(src.abs))
	if dest == src.abs || (isMove && strings.HasPrefix(dest, src.abs+string(filepath.Separator))) {
		return errStatus(http.StatusBadRequest, "不能移动到自身内部")
	}
	if _, err := os.Stat(dest); err == nil {
		return errStatus(http.StatusConflict, "目标位置已存在同名文件")
	}

	if isMove {
		if err := os.Rename(src.abs, dest); err != nil {
			return err
		}
	} else {
		srcSize := dirSize(src.abs)
		if getUsage(true)+srcSize > capBytes {
			return errStatus(http.StatusRequestEntityTooLarge, "复制后超过 20GB 磁盘配额")
		}
		srcInfo, err := os.Stat(src.abs)
		if err != nil {
			return err
		}
		if srcInfo.IsDir() {
			if err := os.Mkdir(dest, 0o755); err != nil {
				return err
			}
			if err := copyTree(src.abs, dest); err != nil {
				return err
			}
		} else if err := copyFile(src.abs, dest, srcInfo.Mode()); err != nil {
			return err
		}
	}
	invalidateUsage()
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func dirSize(p string) int64 {
	var total int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func copyTree(src, dest string) error {
	items, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, d := range items {
		s := filepath.Join(src, d.Name())
		t := filepath.Join(dest, d.Name())
		if d.IsDir() {
			if err := os.Mkdir(t, 0o755); err != nil {
				return err
			}
			if err := copyTree(s, t); err != nil {
				return err
			}
			continue
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := copyFile(s, t, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dest string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return err
	}
	return out.Close()
}
