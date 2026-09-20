package main

import (
	"strings"
	"testing"
	"time"
)

// 私密区的 Cookie 是唯一凭证：签名、过期、篡改三条路径都要钉住。
func TestPrivateToken(t *testing.T) {
	now := time.Unix(1700000000, 0)
	code := "super-secret-code"
	token := makePrivateToken(code, now)

	if !verifyPrivateToken(token, code, now) {
		t.Fatal("刚签发的令牌应该有效")
	}
	// 有效期 12 小时：11 小时后仍有效，13 小时后过期
	if !verifyPrivateToken(token, code, now.Add(11*time.Hour)) {
		t.Fatal("11 小时后仍应在有效期内")
	}
	if verifyPrivateToken(token, code, now.Add(13*time.Hour)) {
		t.Fatal("超过 12 小时应该失效")
	}
	// 换了访问码就认不出来
	if verifyPrivateToken(token, "another-code", now) {
		t.Fatal("换码后不该通过")
	}
	// 篡改过期时间或签名都不行
	parts := strings.SplitN(token, ".", 2)
	forged := "9999999999." + parts[1]
	if verifyPrivateToken(forged, code, now) {
		t.Fatal("改过期时间不该通过")
	}
	if verifyPrivateToken(parts[0]+".AAAA", code, now) {
		t.Fatal("改签名不该通过")
	}
	// 空值、格式不对都不能崩
	if verifyPrivateToken("", code, now) || verifyPrivateToken("abc", code, now) {
		t.Fatal("空值或格式不对应判无效")
	}
	if verifyPrivateToken(token, "", now) {
		t.Fatal("服务端没配访问码时不应放行")
	}
}

// 私密区的路径解析必须和公开区一样严格：越界返回 nil，正常路径落在私密根下。
func TestDecodePathInPrivateRoot(t *testing.T) {
	base := "/opt/file-server/private"

	root := decodePathIn(base, "")
	if root == nil || root.abs != base || root.rel != "/" {
		t.Fatalf("空路径应是私密根目录，实际 %+v", root)
	}

	sub := decodePathIn(base, "/sub/dir")
	if sub == nil || sub.abs != base+"/sub/dir" {
		t.Fatalf("子目录应落在私密根下，实际 %+v", sub)
	}

	// ".." 会被 Clean 掉，解析结果仍在私密根内（不会跑到公开区或 /etc）
	esc := decodePathIn(base, "/../../etc/passwd")
	if esc == nil || !strings.HasPrefix(esc.abs, base+"/") {
		t.Fatalf("越界路径必须落在私密根内，实际 %+v", esc)
	}

	// NUL 一律拒绝
	if decodePathIn(base, "/a\x00b") != nil {
		t.Fatal("带 NUL 的路径应返回 nil")
	}
}
