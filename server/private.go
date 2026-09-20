package main

// 文件服务器的私密区域。
//
// 做法是**独立的一棵目录树**（默认 /opt/file-server/private），不在公开根目录里，
// 所以公开列表天然看不到它，也不靠"过滤某个名字"。解锁用超级码换一个签名 Cookie，
// 之后的列目录 / 下载 / 上传 / 改名 / 删除都复用公开区那套处理器，只换根目录。
//
// 与「管理模式」的区别：管理模式靠 X-Admin-Code 头，管的是公开区；
// 私密区靠 Cookie（因为浏览器下载是直接点链接，带不了自定义头），管的是私密树。

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
	privateCookieName = "file_private"
	privateCookieAge  = 12 * 60 * 60 // 秒，和管理模式一样按半天
)

// makePrivateToken 生成 "过期时间.HMAC" 形式的 Cookie 值。
// 密钥就是私密区访问码本身：不引入额外配置，改码即失效。
func makePrivateToken(code string, now time.Time) string {
	exp := strconv.FormatInt(now.Add(privateCookieAge*time.Second).Unix(), 10)
	return exp + "." + privateSig(exp, code)
}

func privateSig(exp string, code string) string {
	mac := hmac.New(sha256.New, []byte(code))
	mac.Write([]byte("file-private:" + exp))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyPrivateToken 校验 Cookie：签名对且没过期才算解锁。
func verifyPrivateToken(token string, code string, now time.Time) bool {
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
	return hmac.Equal([]byte(parts[1]), []byte(privateSig(parts[0], code)))
}

func isPrivateUnlocked(r *http.Request) bool {
	c, err := r.Cookie(privateCookieName)
	if err != nil {
		return false
	}
	return verifyPrivateToken(c.Value, privateCode, time.Now())
}

func setPrivateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     privateCookieName,
		Value:    makePrivateToken(privateCode, time.Now()),
		Path:     "/",
		MaxAge:   privateCookieAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearPrivateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     privateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// routePrivate 处理 /api/private/ 下的所有接口。
func routePrivate(w http.ResponseWriter, r *http.Request, endpoint string) error {
	switch {
	case r.Method == http.MethodPost && endpoint == "/api/private/unlock":
		body, err := readJSON(r)
		if err != nil {
			return err
		}
		if privateCode == "" {
			return errStatus(http.StatusServiceUnavailable, "服务端没有配置私密区域访问码")
		}
		code := str(body, "code")
		if code == "" || !safeEqual(code, privateCode) {
			return errStatus(http.StatusUnauthorized, "超级码不正确")
		}
		setPrivateCookie(w)
		sendJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil

	case r.Method == http.MethodPost && endpoint == "/api/private/lock":
		clearPrivateCookie(w)
		sendJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil

	case r.Method == http.MethodGet && endpoint == "/api/private/session":
		sendJSON(w, http.StatusOK, map[string]any{
			"configured": privateCode != "",
			"unlocked":   isPrivateUnlocked(r),
		})
		return nil
	}

	if !isPrivateUnlocked(r) {
		return errStatus(http.StatusUnauthorized, "私密区域未解锁")
	}

	switch {
	case r.Method == http.MethodGet && endpoint == "/api/private/list":
		return handleList(w, r, privatePaths)
	case r.Method == http.MethodGet && endpoint == "/api/private/usage":
		sendJSON(w, http.StatusOK, usageResponse{Used: privatePaths.used(false), Cap: capBytes})
		return nil
	case r.Method == http.MethodGet && endpoint == "/api/private/download":
		return handleDownload(w, r, privatePaths)
	case r.Method == http.MethodPost && endpoint == "/api/private/mkdir":
		return handleMkdir(w, r, privatePaths)
	case r.Method == http.MethodPost && endpoint == "/api/private/upload":
		return handleUpload(w, r, privatePaths)
	case r.Method == http.MethodPost && endpoint == "/api/private/delete":
		return handleDelete(w, r, privatePaths)
	case r.Method == http.MethodPost && endpoint == "/api/private/rename":
		return handleRename(w, r, privatePaths)
	case r.Method == http.MethodPost && (endpoint == "/api/private/move" || endpoint == "/api/private/copy"):
		return handleMoveCopy(w, r, endpoint == "/api/private/move", privatePaths)
	}
	return errStatus(http.StatusNotFound, "接口不存在")
}
