package main

// 统一登录签发的会话在各站使用同一签名密钥；文件站仍自行决定只读与管理权限。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const sessionCookieName = "tykrem_session"
const sessionCookieAge = 12 * time.Hour

type sessionClaims struct {
	Version int    `json:"v"`
	Role    string `json:"role"`
	Expires int64  `json:"exp"`
}

var sessionSecret string

func verifySessionToken(token, secret string, now time.Time) string {
	if secret == "" || len(token) > 512 {
		return ""
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(actual, expected) {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ""
	}
	var claims sessionClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Version != 1 ||
		(claims.Role != "admin" && claims.Role != "read") || claims.Expires <= now.Unix() ||
		claims.Expires > now.Add(sessionCookieAge).Unix() {
		return ""
	}
	return claims.Role
}

func isUnlocked(r *http.Request) bool { return sessionRole(r) != "" }

func sessionRole(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return verifySessionToken(cookie.Value, sessionSecret, time.Now())
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Domain: "tykrem.top", Path: "/",
		MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}
