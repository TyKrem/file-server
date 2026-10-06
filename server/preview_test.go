package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImagePreviewPermissionsAndContent(t *testing.T) {
	previousRoot, previousSecret, previousPaths := root, sessionSecret, filePaths
	root, sessionSecret = t.TempDir(), "isolated-preview-session-secret-32-characters"
	filePaths = rootPaths{dir: root, real: root}
	defer func() { root, sessionSecret, filePaths = previousRoot, previousSecret, previousPaths }()
	bitmap := image.NewRGBA(image.Rect(0, 0, 4, 4))
	bitmap.Set(1, 1, color.RGBA{R: 80, B: 200, A: 255})
	var content bytes.Buffer
	if err := png.Encode(&content, bitmap); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "图 1.PNG"), content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"伪装.png": "<html><script>alert(1)</script></html>", "矢量.svg": "<svg/>", "正文.txt": "测试"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.png")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, filename, role string
		status               int
	}{
		{"未登录拒绝", "/图 1.PNG", "", 401},
		{"只读可预览", "/图 1.PNG", "read", 200},
		{"管理可预览", "/图 1.PNG", "admin", 200},
		{"假图片拒绝", "/伪装.png", "read", 415},
		{"矢量仍下载", "/矢量.svg", "read", 415},
		{"普通文档拒绝", "/正文.txt", "read", 415},
		{"缺失图片", "/不存在.png", "read", 404},
		{"符号链接越界", "/link.png", "read", 403},
		{"上级路径仍限制在根内", "/../outside.png", "read", 404},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/preview?path="+url.QueryEscape(item.filename), nil)
			if item.role != "" {
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testSessionToken(item.role, sessionSecret, time.Now())})
			}
			response := httptest.NewRecorder()
			handleAPI(response, request)
			if response.Code != item.status {
				t.Fatalf("状态 %d，预期 %d：%s", response.Code, item.status, response.Body.String())
			}
			if item.status == 200 {
				if !bytes.Equal(response.Body.Bytes(), content.Bytes()) {
					t.Fatal("预览字节与原图不一致")
				}
				if response.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "inline;") {
					t.Fatal("图片应以正确类型内联返回")
				}
				if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Fatal("预览响应头不正确")
				}
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/api/preview?path="+url.QueryEscape("/图 1.PNG"), nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testSessionToken("read", sessionSecret, time.Now())})
	request.Header.Set("Range", "bytes=0-7")
	response := httptest.NewRecorder()
	handleAPI(response, request)
	if response.Code != 206 || !bytes.Equal(response.Body.Bytes(), content.Bytes()[:8]) {
		t.Fatal("图片分段读取失败")
	}
	large, err := os.Create(filepath.Join(root, "large.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(maxPreviewBytes + 1); err != nil {
		t.Fatal(err)
	}
	large.Close()
	request = httptest.NewRequest(http.MethodGet, "/api/preview?path=/large.png", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testSessionToken("read", sessionSecret, time.Now())})
	response = httptest.NewRecorder()
	handleAPI(response, request)
	if response.Code != 413 {
		t.Fatalf("超大图片应被拒绝，实际 %d", response.Code)
	}
}
