package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxPreviewBytes = int64(64 << 20)

// 只内联浏览器可解码的位图；SVG、HTML 等可执行文档仍走下载。
func previewContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/vnd.microsoft.icon"
	default:
		return ""
	}
}

func handleImagePreview(w http.ResponseWriter, r *http.Request, paths rootPaths) error {
	info := decodePathIn(paths.dir, r.URL.Query().Get("path"))
	if info == nil {
		return errStatus(http.StatusBadRequest, "图片路径无效")
	}
	real, err := ensureInsideIn(paths.real, info.abs)
	if err != nil {
		return err
	}
	expected := previewContentType(info.abs)
	if expected == "" {
		return errStatus(http.StatusUnsupportedMediaType, "该类型不支持图片预览，请下载查看")
	}
	file, err := os.Open(real)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() {
		return errStatus(http.StatusBadRequest, "只能预览普通图片文件")
	}
	if stat.Size() > maxPreviewBytes {
		return errStatus(http.StatusRequestEntityTooLarge, "图片超过 64MB，请下载查看")
	}
	header := make([]byte, 512)
	count, err := file.Read(header)
	if err != nil && err != io.EOF {
		return err
	}
	if http.DetectContentType(header[:count]) != expected {
		return errStatus(http.StatusUnsupportedMediaType, "文件内容与图片类型不符，请下载查看")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.Header().Set("Content-Type", expected)
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+rfc5987Encode(filepath.Base(info.abs)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, filepath.Base(info.abs), stat.ModTime(), file)
	return nil
}
