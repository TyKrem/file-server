package main

// 把启动与 5xx 报到日志中心（log.tykrem.top）。
//
// 令牌优先用环境变量 FILE_LOG_TOKEN，没有就读 /etc/log-app.env 的 LOG_INGEST_TOKEN
// ——同机服务共用一份写入令牌，省得再往 /etc/file-server.env 里抄一遍。
// 只报「启动」与「服务端故障」，不报每次请求：这个站是公开的，逐请求上报会把日志刷爆。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const logCenterEndpoint = "http://127.0.0.1:8792/api/v1/logs"

var (
	logTokenOnce sync.Once
	logTokenVal  string
)

// logToken 取上报令牌；取不到就返回空串（调用方直接跳过）。
func logToken() string {
	logTokenOnce.Do(func() {
		if v := strings.TrimSpace(os.Getenv("FILE_LOG_TOKEN")); v != "" {
			logTokenVal = v
			return
		}
		raw, err := os.ReadFile("/etc/log-app.env")
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "LOG_INGEST_TOKEN=") {
				logTokenVal = strings.TrimSpace(strings.TrimPrefix(line, "LOG_INGEST_TOKEN="))
				return
			}
		}
	})
	return logTokenVal
}

// reportLog 异步上报一条日志；失败静默（不能让日志问题影响文件服务本身）。
func reportLog(level, message string, meta map[string]any) {
	token := logToken()
	if token == "" {
		return
	}
	body, err := json.Marshal(map[string]any{
		"source":  "file-server",
		"level":   level,
		"message": message,
		"meta":    meta,
	})
	if err != nil {
		return
	}
	go func() {
		req, err := http.NewRequest("POST", logCenterEndpoint, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Log-Token", token)
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		resp.Body.Close()
	}()
}
