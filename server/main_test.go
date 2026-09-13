package main

import (
	"path/filepath"
	"testing"
)

func TestDecodePath(t *testing.T) {
	oldRoot := root
	root = "/srv/files"
	defer func() { root = oldRoot }()

	ok := []struct{ in, want string }{
		{"", "/srv/files"},
		{"/", "/srv/files"},
		{"/a/b", "/srv/files/a/b"},
		{"a/b", "/srv/files/a/b"},    // 没有前导斜杠也接受
		{`/a\b`, "/srv/files/a/b"},   // 反斜杠当分隔符
		{"/a/./b", "/srv/files/a/b"}, // 规范化
		{"/中文/文件.txt", "/srv/files/中文/文件.txt"},
	}
	for _, c := range ok {
		got := decodePath(c.in)
		if got == nil {
			t.Errorf("decodePath(%q) = nil，期望 %s", c.in, c.want)
			continue
		}
		if got.abs != c.want {
			t.Errorf("decodePath(%q).abs = %s，期望 %s", c.in, got.abs, c.want)
		}
	}

	// 根目录之上的 .. 会被夹回根目录（Node 的 path.normalize 同样如此），
	// 这里要保证的不是「拒绝」，而是「结果必须仍在根目录内」。
	clamped := []struct{ in, want string }{
		{"/../etc/passwd", "/srv/files/etc/passwd"},
		{"/a/../../etc", "/srv/files/etc"},
		{"/..", "/srv/files"},
		{"/a/b/../../../..", "/srv/files"},
	}
	for _, c := range clamped {
		got := decodePath(c.in)
		if got == nil {
			t.Errorf("decodePath(%q) = nil，期望夹到 %s", c.in, c.want)
			continue
		}
		if got.abs != c.want {
			t.Errorf("decodePath(%q).abs = %s，期望 %s", c.in, got.abs, c.want)
		}
	}

	// 含 NUL 的一律拒绝
	for _, in := range []string{"\x00", "/a\x00b"} {
		if got := decodePath(in); got != nil {
			t.Errorf("decodePath(%q) 含 NUL，应当拒绝，却得到 %s", in, got.abs)
		}
	}
}

// /a/.. 会被规范成根目录，属于合法输入（不是越界）
func TestDecodePathDotDotToRoot(t *testing.T) {
	oldRoot := root
	root = "/srv/files"
	defer func() { root = oldRoot }()

	got := decodePath("/a/..")
	if got == nil || got.abs != filepath.Clean("/srv/files") {
		t.Fatalf("decodePath(/a/..) = %v，期望回到根目录", got)
	}
}

func TestRFC5987Encode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"自设.jpg", "%E8%87%AA%E8%AE%BE.jpg"},
		{"plain.txt", "plain.txt"},
		{"a b.txt", "a%20b.txt"},
		// encodeURIComponent 不转 ' ( )，原来的 Node 版会额外转掉，这里要对齐
		{"it's(1).txt", "it%27s%281%29.txt"},
		// 反斜杠必须转义，这个字符曾经被漏掉过
		{`back\slash.txt`, "back%5Cslash.txt"},
		{"中文名 带空格.txt", "%E4%B8%AD%E6%96%87%E5%90%8D%20%E5%B8%A6%E7%A9%BA%E6%A0%BC.txt"},
	}
	for _, c := range cases {
		if got := rfc5987Encode(c.in); got != c.want {
			t.Errorf("rfc5987Encode(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// 排序分档要跟 Node 的 localeCompare(x, 'zh-CN') 一致：
// 符号 < 数字 < 汉字 < 拉丁 < 其他文字
func TestSortRank(t *testing.T) {
	order := []string{"_x.txt", "1.txt", "笔记.txt", "a.png", "ZZ.txt", "Αλφα.txt", "あ.txt"}
	for i := 0; i < len(order)-1; i++ {
		if sortRank(order[i]) > sortRank(order[i+1]) {
			t.Errorf("分档顺序不对：%q(%d) 应排在 %q(%d) 之前",
				order[i], sortRank(order[i]), order[i+1], sortRank(order[i+1]))
		}
	}
}

func TestContentType(t *testing.T) {
	cases := map[string]string{
		"a.txt":   "text/plain; charset=utf-8",
		"A.PNG":   "image/png",
		"b.md":    "text/plain; charset=utf-8",
		"c.weird": "application/octet-stream",
		"noext":   "application/octet-stream",
	}
	for name, want := range cases {
		if got := contentType(name); got != want {
			t.Errorf("contentType(%q) = %q，期望 %q", name, got, want)
		}
	}
}

func TestSafeEqual(t *testing.T) {
	if !safeEqual("abc123", "abc123") {
		t.Error("相同字符串应当相等")
	}
	if safeEqual("abc123", "abc124") {
		t.Error("不同字符串不应当相等")
	}
	if safeEqual("abc", "abc123") {
		t.Error("长度不同不应当相等")
	}
}
