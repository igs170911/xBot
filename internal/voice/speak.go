package voice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"xbot/internal/limits"
)

// audioMeta 是快取裡一段音訊的資料（<id>.json）。
type audioMeta struct {
	BotID       string `json:"bot_id"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
	Chars       int    `json:"chars"`
	CreatedAt   int64  `json:"created_at"`
}

var audioIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidAudioID 檢查音訊代號的格式（避免拿來讀別的檔案）。
func ValidAudioID(id string) bool { return audioIDPattern.MatchString(id) }

// synthRequest 是一次合成要送的東西。
type synthRequest struct {
	BotID  string
	Engine Engine
	Voice  string // 供應商的聲音名稱
	Speed  float64
	Text   string
}

// key 是快取用的雜湊：同一個 Bot、引擎、聲音、語速、文字念出來一樣。
func (r synthRequest) key() string {
	h := sha256.New()
	for _, part := range []string{r.BotID, r.Engine.Provider, r.Engine.Model, r.Voice, strconv.FormatFloat(r.Speed, 'f', 2, 64), "mp3", r.Text} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func (s *Service) audioPath(id string) string { return filepath.Join(s.CacheDir, id+".mp3") }
func (s *Service) metaPath(id string) string  { return filepath.Join(s.CacheDir, id+".json") }

// Audio 讀快取裡一段音訊的資料；沒有時回 os.ErrNotExist。
func (s *Service) Audio(id string) (audioMeta, string, error) {
	if !ValidAudioID(id) || s.CacheDir == "" {
		return audioMeta{}, "", os.ErrNotExist
	}
	raw, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return audioMeta{}, "", err
	}
	var m audioMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return audioMeta{}, "", os.ErrNotExist
	}
	p := s.audioPath(id)
	if _, err := os.Stat(p); err != nil {
		return audioMeta{}, "", err
	}
	return m, p, nil
}

// synth 合成（或從快取拿）一段音訊；同一段同時有人要時只送一次。
func (s *Service) synth(ctx context.Context, req synthRequest) (string, audioMeta, bool, error) {
	id := req.key()
	if m, _, err := s.Audio(id); err == nil {
		now := time.Now()
		_ = os.Chtimes(s.audioPath(id), now, now) // 最近用過的留久一點
		return id, m, true, nil
	}
	s.mu.Lock()
	if s.inflight == nil {
		s.inflight = map[string]*flight{}
	}
	if f, ok := s.inflight[id]; ok {
		s.mu.Unlock()
		select {
		case <-f.done:
			return id, f.meta, true, f.err
		case <-ctx.Done():
			return id, audioMeta{}, false, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	s.inflight[id] = f
	s.mu.Unlock()
	f.meta, f.err = s.fetch(ctx, id, req)
	s.mu.Lock()
	delete(s.inflight, id)
	s.mu.Unlock()
	close(f.done)
	return id, f.meta, false, f.err
}

func (s *Service) fetch(ctx context.Context, id string, req synthRequest) (audioMeta, error) {
	if s.Media == nil {
		return audioMeta{}, ErrNoEngine
	}
	body, _ := json.Marshal(map[string]any{
		"model": req.Engine.Model, "input": req.Text, "voice": req.Voice, "speed": req.Speed, "response_format": "mp3",
	})
	ctx, cancel := context.WithTimeout(ctx, limits.VoiceRequestTimeout)
	defer cancel()
	resp, err := s.Media(ctx, MediaRequest{Provider: req.Engine.Provider, Path: "audio/speech", ContentType: "application/json",
		Body: body, BotID: req.BotID, MaxBytes: limits.VoiceAudioBytes})
	if err != nil {
		return audioMeta{}, err
	}
	if resp.Status >= 400 {
		return audioMeta{}, &UpstreamError{Status: resp.Status, Message: upstreamMessage(resp.Body)}
	}
	if len(resp.Body) == 0 {
		return audioMeta{}, &UpstreamError{Status: resp.Status, Message: "供應商沒有回傳聲音"}
	}
	ct := resp.ContentType
	if !strings.HasPrefix(ct, "audio/") {
		ct = "audio/mpeg"
	}
	m := audioMeta{BotID: req.BotID, ContentType: ct, Bytes: int64(len(resp.Body)), Chars: len([]rune(req.Text)), CreatedAt: time.Now().UnixMilli()}
	if err := s.store(id, resp.Body, m); err != nil {
		return audioMeta{}, err
	}
	return m, nil
}

// store 把音訊寫進快取（先寫暫存檔再改名，讀的人不會讀到一半），然後把快取控制在上限內。
func (s *Service) store(id string, audio []byte, m audioMeta) error {
	if err := os.MkdirAll(s.CacheDir, 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(m)
	for _, f := range []struct {
		path string
		data []byte
	}{{s.audioPath(id), audio}, {s.metaPath(id), raw}} {
		tmp, err := os.CreateTemp(s.CacheDir, ".tmp-*")
		if err != nil {
			return err
		}
		if _, err := tmp.Write(f.data); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmp.Name())
			return err
		}
		if err := os.Rename(tmp.Name(), f.path); err != nil {
			os.Remove(tmp.Name())
			return err
		}
	}
	s.prune(limits.VoiceCacheBytes)
	return nil
}

// prune 快取超過 max 時，從最久沒用的開始刪。
func (s *Service) prune(max int64) {
	entries, err := os.ReadDir(s.CacheDir)
	if err != nil {
		return
	}
	type item struct {
		id   string
		size int64
		at   time.Time
	}
	var items []item
	var total int64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".mp3") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{strings.TrimSuffix(name, ".mp3"), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= max {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	for _, it := range items {
		if total <= max {
			break
		}
		_ = os.Remove(s.audioPath(it.id))
		_ = os.Remove(s.metaPath(it.id))
		total -= it.size
	}
}

// UpstreamError 是供應商回的錯誤。
type UpstreamError struct {
	Status  int
	Message string
}

func (e *UpstreamError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("語音引擎回了錯誤（%d）", e.Status)
	}
	return fmt.Sprintf("語音引擎回了錯誤（%d）：%s", e.Status, e.Message)
}

// upstreamMessage 讀出 OpenAI 格式的錯誤訊息；不是 JSON 時取前 200 字。
func upstreamMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return clipRunes(e.Error.Message, 200)
	}
	return clipRunes(strings.TrimSpace(string(body)), 200)
}

func clipRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

// isBudget 回答錯誤是不是超過花費上限。
func isBudget(err error) (*BudgetError, bool) {
	var be *BudgetError
	ok := errors.As(err, &be)
	return be, ok
}
