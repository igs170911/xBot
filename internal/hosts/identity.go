package hosts

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// IdentityRef 是 XBot 的專用 SSH 私鑰在資料目錄裡的位置（host.key_ref）。
const IdentityRef = "ssh/id_ed25519"

// Identity 是 XBot 連遠端主機用的 SSH 金鑰（每個 XBot 一把，放在資料目錄，權限 0600）。
type Identity struct {
	Signer ssh.Signer
	// AuthorizedKey 是放進遠端 authorized_keys 的那一行（含註解）。
	AuthorizedKey string
	// Path 是私鑰檔。
	Path string
}

// Fingerprint 是公鑰的 SHA256 指紋（給人比對用）。
func (i Identity) Fingerprint() string { return ssh.FingerprintSHA256(i.Signer.PublicKey()) }

// EnsureIdentity 讀 <data>/ssh/id_ed25519；第一次用時產生一把 ed25519 金鑰（0600）。可以重複呼叫。
func EnsureIdentity(dataDir string) (Identity, error) {
	path := filepath.Join(dataDir, filepath.FromSlash(IdentityRef))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if data, err = newIdentity(path); err != nil {
			return Identity{}, fmt.Errorf("無法產生 XBot 的 SSH 金鑰：%w", err)
		}
	} else if err != nil {
		return Identity{}, fmt.Errorf("無法讀取 XBot 的 SSH 金鑰 %s：%w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return Identity{}, fmt.Errorf("XBot 的 SSH 金鑰 %s 壞掉了：%w", path, err)
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(path, 0o600)
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "xbot"
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " xbot@" + host
	return Identity{Signer: signer, AuthorizedKey: line, Path: path}, nil
}

func newIdentity(path string) ([]byte, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "xbot")
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(block)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// 先寫好暫存檔再 link 成正式檔名：兩個程序同時第一次使用時，只有一個 link 得上，另一個讀它寫好的完整金鑰。
	tmp, err := os.CreateTemp(filepath.Dir(path), ".id_ed25519-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmp.Name(), path); errors.Is(err, os.ErrExist) {
		return os.ReadFile(path)
	} else if err != nil {
		return nil, err
	}
	return data, nil
}
