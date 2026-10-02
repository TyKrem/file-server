package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSessionToken(role, secret string, now time.Time) string {
	payload, _ := json.Marshal(sessionClaims{Version: 1, Role: role, Expires: now.Add(sessionCookieAge).Unix()})
	part := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(part))
	return part + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// 会话 Cookie 是全站唯一凭证：签名、过期、篡改三条路径都要钉住。
func TestSessionToken(t *testing.T) {
	now := time.Unix(1700000000, 0)
	secret := "super-secret-code"
	token := testSessionToken("read", secret, now)

	if verifySessionToken(token, secret, now) != "read" {
		t.Fatal("刚签发的令牌应该有效")
	}
	// 一周内可继续使用，超过一周必须失效。
	if verifySessionToken(token, secret, now.Add(6*24*time.Hour)) != "read" {
		t.Fatal("第 6 天仍应在有效期内")
	}
	if verifySessionToken(token, secret, now.Add(8*24*time.Hour)) != "" {
		t.Fatal("超过一周应该失效")
	}
	// 换了访问码就认不出来
	if verifySessionToken(token, "another-code", now) != "" {
		t.Fatal("换码后不该通过")
	}
	// 篡改过期时间或签名都不行
	parts := strings.SplitN(token, ".", 2)
	forged := "9999999999." + parts[1]
	if verifySessionToken(forged, secret, now) != "" {
		t.Fatal("改过期时间不该通过")
	}
	if verifySessionToken(parts[0]+".AAAA", secret, now) != "" {
		t.Fatal("改签名不该通过")
	}
	// 空值、格式不对都不能崩
	if verifySessionToken("", secret, now) != "" || verifySessionToken("abc", secret, now) != "" {
		t.Fatal("空值或格式不对应判无效")
	}
	if verifySessionToken(token, "", now) != "" {
		t.Fatal("服务端没配访问码时不应放行")
	}
}

func TestReadOnlyCannotWrite(t *testing.T) {
	previousSecret := sessionSecret
	sessionSecret = "test-session-secret"
	defer func() { sessionSecret = previousSecret }()

	request := httptest.NewRequest(http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/x"}`))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testSessionToken("read", sessionSecret, time.Now())})
	response := httptest.NewRecorder()
	handleAPI(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("只读会话写入应返回 403，实际 %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testSessionToken("read", sessionSecret, time.Now())})
	response = httptest.NewRecorder()
	handleAPI(response, request)
	if !strings.Contains(response.Body.String(), `"role":"read"`) {
		t.Fatalf("会话应标识为只读：%s", response.Body.String())
	}
}

func TestFileSiteOriginsAndPermissions(t *testing.T) {
	previousRoot, previousSecret, previousPaths := root, sessionSecret, filePaths
	root, sessionSecret = t.TempDir(), "test-session-secret"
	filePaths = rootPaths{dir: root, real: root}
	defer func() { root, sessionSecret, filePaths = previousRoot, previousSecret, previousPaths }()

	cases := []struct {
		name   string
		origin string
		role   string
		status int
	}{
		{"主入口管理会话", "https://file.tykrem.top", "admin", http.StatusOK},
		{"IPv4入口管理会话", "https://file4.tykrem.top", "admin", http.StatusOK},
		{"IPv4入口只读会话", "https://file4.tykrem.top", "read", http.StatusForbidden},
		{"IPv4入口无会话", "https://file4.tykrem.top", "", http.StatusUnauthorized},
		{"其他来源", "https://evil.example", "admin", http.StatusForbidden},
		{"伪装子域名", "https://file4.tykrem.top.evil.example", "admin", http.StatusForbidden},
		{"不安全协议", "http://file4.tykrem.top", "admin", http.StatusForbidden},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/mkdir", strings.NewReader(`{"path":"/","name":"`+item.name+`"}`))
			request.Header.Set("Origin", item.origin)
			if item.role != "" {
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testSessionToken(item.role, sessionSecret, time.Now())})
			}
			response := httptest.NewRecorder()
			handleAPI(response, request)
			if response.Code != item.status {
				t.Fatalf("请求状态应为 %d，实际 %d：%s", item.status, response.Code, response.Body.String())
			}
			_, err := os.Stat(filepath.Join(root, item.name))
			if item.status == http.StatusOK && err != nil {
				t.Fatalf("允许的写入应创建目录：%v", err)
			}
			if item.status != http.StatusOK && !os.IsNotExist(err) {
				t.Fatalf("被拒绝的写入不应创建目录，实际错误：%v", err)
			}
		})
	}
}

// 路径解析必须严格：越界返回 nil，正常路径落在给定根下。
func TestDecodePathInRoot(t *testing.T) {
	base := "/opt/file-server/root"

	root := decodePathIn(base, "")
	if root == nil || root.abs != base || root.rel != "/" {
		t.Fatalf("空路径应是根目录，实际 %+v", root)
	}

	sub := decodePathIn(base, "/a/b.txt")
	if sub == nil || sub.abs != base+"/a/b.txt" {
		t.Fatalf("子路径应落在根下，实际 %+v", sub)
	}

	// ".." 会被 Clean 掉，解析结果仍在根内（不会跑到 /etc）
	for _, raw := range []string{"/../../etc/passwd", "/..", "/a/../../../.."} {
		got := decodePathIn(base, raw)
		if got == nil {
			t.Fatalf("decodePathIn(%q) = nil", raw)
		}
		if !strings.HasPrefix(got.abs, base+"/") && got.abs != base {
			t.Fatalf("decodePathIn(%q) 越界：%s", raw, got.abs)
		}
	}

	if decodePathIn(base, "/a\x00b") != nil {
		t.Fatal("含 NUL 的路径必须拒绝")
	}
}
