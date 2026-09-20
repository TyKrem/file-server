package main

import (
	"strings"
	"testing"
	"time"
)

// 会话 Cookie 是全站唯一凭证：签名、过期、篡改三条路径都要钉住。
func TestSessionToken(t *testing.T) {
	now := time.Unix(1700000000, 0)
	code := "super-secret-code"
	token := makeSessionToken(code, now)

	if !verifySessionToken(token, code, now) {
		t.Fatal("刚签发的令牌应该有效")
	}
	// 有效期 12 小时：11 小时后仍有效，13 小时后过期
	if !verifySessionToken(token, code, now.Add(11*time.Hour)) {
		t.Fatal("11 小时后仍应在有效期内")
	}
	if verifySessionToken(token, code, now.Add(13*time.Hour)) {
		t.Fatal("超过 12 小时应该失效")
	}
	// 换了访问码就认不出来
	if verifySessionToken(token, "another-code", now) {
		t.Fatal("换码后不该通过")
	}
	// 篡改过期时间或签名都不行
	parts := strings.SplitN(token, ".", 2)
	forged := "9999999999." + parts[1]
	if verifySessionToken(forged, code, now) {
		t.Fatal("改过期时间不该通过")
	}
	if verifySessionToken(parts[0]+".AAAA", code, now) {
		t.Fatal("改签名不该通过")
	}
	// 空值、格式不对都不能崩
	if verifySessionToken("", code, now) || verifySessionToken("abc", code, now) {
		t.Fatal("空值或格式不对应判无效")
	}
	if verifySessionToken(token, "", now) {
		t.Fatal("服务端没配访问码时不应放行")
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
