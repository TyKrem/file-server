package main

// 全站鉴权：整个文件服务都要超级码，输一次换一个 12 小时有效的签名 Cookie。
//
// 用 Cookie 而不是每个请求带 X-Admin-Code 头，是因为浏览器点下载链接时加不了
// 自定义请求头；签名密钥就是访问码本身，改码即让所有旧 Cookie 失效，
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
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return verifySessionToken(c.Value, adminCode, time.Now())
}

func setSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    makeSessionToken(adminCode, time.Now()),
		Path:     "/",
		MaxAge:   sessionCookieAge,
		HttpOnly: true,
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
		SameSite: http.SameSiteStrictMode,
	})
}
