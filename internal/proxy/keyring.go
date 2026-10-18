package proxy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// KeyFile 是 Model Proxy 主金鑰在資料目錄中的檔名（32 位元組，權限 600）。
const KeyFile = "model-proxy.key"

const masterKeyBytes = 32

// sealVersion 是密文開頭的版本位元組，之後換演算法時用來分辨。
const sealVersion byte = 1

// Keyring 由一把主金鑰衍生出兩把用途分開的子金鑰：
// 一把用 AES-GCM 加密存放的供應商金鑰，一把用來簽 worker-key（HMAC-SHA256）。
type Keyring struct {
	aead      cipher.AEAD
	workerKey []byte
}

// LoadKeyring 讀資料目錄裡的主金鑰；不存在就產生一把並以 600 權限寫入。
func LoadKeyring(dataDir string) (*Keyring, error) {
	p := filepath.Join(dataDir, KeyFile)
	master, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		master, err = createMasterKey(dataDir, p)
	}
	if err != nil {
		return nil, fmt.Errorf("讀不到模型代理金鑰 %s：%w", p, err)
	}
	// 還原備份等情況可能放寬了權限；收回成只有自己能讀。
	if info, err := os.Stat(p); err == nil && info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(p, 0o600); err != nil {
			return nil, fmt.Errorf("無法把 %s 改成只有自己能讀：%w", p, err)
		}
	}
	return NewKeyring(master)
}

func createMasterKey(dataDir, p string) ([]byte, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, masterKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		// 另一個行程剛好先建好了，用它的。
		return os.ReadFile(p)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		_ = os.Remove(p)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(p)
		return nil, err
	}
	return key, nil
}

// NewKeyring 用一把 32 位元組的主金鑰建立 Keyring。
func NewKeyring(master []byte) (*Keyring, error) {
	if len(master) != masterKeyBytes {
		return nil, fmt.Errorf("模型代理金鑰長度不對（%d 位元組，應為 %d）", len(master), masterKeyBytes)
	}
	block, err := aes.NewCipher(derive(master, "xbot/model-proxy/secret-box/v1"))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Keyring{aead: aead, workerKey: derive(master, "xbot/model-proxy/worker-key/v1")}, nil
}

func derive(master []byte, label string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte(label))
	return m.Sum(nil)
}

// Seal 加密一段祕密。aad 綁定用途（例：provider:openai），密文搬到別的欄位就解不開。
func (k *Keyring) Seal(plaintext []byte, aad string) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+k.aead.Overhead())
	out = append(out, sealVersion)
	out = append(out, nonce...)
	return k.aead.Seal(out, nonce, plaintext, []byte(aad)), nil
}

// ErrSealed 表示密文壞了、被改過，或不是用這把金鑰加密的。
var ErrSealed = errors.New("加密資料無法解開（金鑰不對或資料損壞）")

// Open 解開 Seal 的結果。
func (k *Keyring) Open(sealed []byte, aad string) ([]byte, error) {
	ns := k.aead.NonceSize()
	if len(sealed) < 1+ns+k.aead.Overhead() || sealed[0] != sealVersion {
		return nil, ErrSealed
	}
	pt, err := k.aead.Open(nil, sealed[1:1+ns], sealed[1+ns:], []byte(aad))
	if err != nil {
		return nil, ErrSealed
	}
	return pt, nil
}
