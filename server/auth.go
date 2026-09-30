package main

// 全站鉴权：访问码只读，管理码可修改文件；两种会话互不通用。
//
// 用 Cookie 而不是每个请求带 X-Admin-Code 头，是因为浏览器点下载链接时加不了
// 自定义请求头；签名密钥就是对应角色的访问码，改码即让旧 Cookie 失效，
// 不额外引入配置。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "file_session"
	sessionCookieAge  = 12 * 60 * 60 // 秒，按半天
)

// makeSessionToken 生成 "过期时间.HMAC" 形式的 Cookie 值。
func makeSessionToken(code string, now time.Time) string {
	exp := strconv.FormatInt(now.Add(sessionCookieAge*time.Second).Unix(), 10)
	return exp + "." + sessionSig(exp, code)
}

func sessionSig(exp string, code string) string {
	mac := hmac.New(sha256.New, []byte(code))
	mac.Write([]byte("file-session:" + exp))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifySessionToken 校验 Cookie：签名对且没过期才算解锁。
func verifySessionToken(token string, code string, now time.Time) bool {
	if code == "" || token == "" {
		return false
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(parts[1]), []byte(sessionSig(parts[0], code)))
}

func isUnlocked(r *http.Request) bool {
	return sessionRole(r) != ""
}

func sessionRole(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	if verifySessionToken(c.Value, adminCode, time.Now()) {
		return "admin"
	}
	if verifySessionToken(c.Value, readCode, time.Now()) {
		return "read"
	}
	return ""
}

func setSessionCookie(w http.ResponseWriter, code string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    makeSessionToken(code, time.Now()),
		Path:     "/",
		MaxAge:   sessionCookieAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}
