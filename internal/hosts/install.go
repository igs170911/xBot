package hosts

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"strconv"
	"strings"

	"xbot/internal/limits"
)

//go:embed install.sh
var installScript string

// InstallScript 是在要加入的主機上用 root 執行的安裝腳本（裝 Docker、Tailscale，建 xbot 帳號、放公鑰、設定 sshd）。
// authorizedKey 是 XBot 的公鑰那一行（Identity.AuthorizedKey）。腳本可以重複執行。
func InstallScript(authorizedKey string) string {
	r := strings.NewReplacer(
		"__XBOT_USER__", limits.HostSSHUser,
		"__XBOT_AUTHORIZED_KEY__", strings.ReplaceAll(strings.TrimSpace(authorizedKey), "'", ""),
		"__XBOT_DATA_DIR__", limits.SSHRemoteDataDir,
		"__XBOT_DOCKER_MIN__", strconv.Itoa(limits.HostMinDockerMajor),
	)
	return r.Replace(installScript)
}

// InstallArgs 是安裝腳本的參數。
func InstallArgs(tailscale bool) []string {
	if tailscale {
		return nil
	}
	return []string{"--no-tailscale"}
}

// InstallCommand 把安裝腳本壓成「貼一行就好」的指令（J11 步驟 1）：
//
//	echo '<gzip＋base64>' | base64 -d | gunzip | sudo sh -s -- [--no-tailscale]
//
// 不需要主機連得到 XBot（不用另開下載網址），內容只有公鑰，沒有秘密。
func InstallCommand(authorizedKey string, tailscale bool) string {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write([]byte(InstallScript(authorizedKey)))
	_ = zw.Close()
	cmd := "echo '" + base64.StdEncoding.EncodeToString(buf.Bytes()) + "' | base64 -d | gunzip | sudo sh -s --"
	for _, a := range InstallArgs(tailscale) {
		cmd += " " + a
	}
	return cmd
}
