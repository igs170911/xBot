// Package push 是 Web Push（W31；PRD §19.2「XBot 加 Web Push」、MB-1、畫板 L06、L14）：
// 手機或電腦的瀏覽器訂閱後，「待我處理」出現要你決定的新卡（核准、提問…）就推播到裝置上；
// 勾了「工作完成也通知」的裝置，工作完成卡也會推。
//
// VAPID 金鑰第一次用到時產生，存在設定表 push.vapid；訂閱存在 push.subscriptions（不另開資料表）。
// 推播內容只有標題、一句分類和要打開的網址；不放卡片內文，密碼類（secret）卡片更不會帶任何內容。
package push

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/store"
)

// 設定表的 key。
const (
	keyVAPID = "push.vapid"
	keySubs  = "push.subscriptions"
)

// DefaultSubject 是 VAPID 的聯絡方式（JWT 的 sub）。XBOT_PUSH_SUBJECT 可以改成自己的 email 或 https 網址。
const DefaultSubject = "mailto:xbot@localhost"

type vapid struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// Subscription 是一個裝置的推播訂閱。
type Subscription struct {
	ID         string `json:"id"`
	Endpoint   string `json:"endpoint"`
	P256dh     string `json:"p256dh"`
	Auth       string `json:"auth"`
	Label      string `json:"label,omitempty"`
	NotifyDone bool   `json:"notify_done,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// Message 是推播的內容（service worker 收到後顯示成通知）。
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
	Kind  string `json:"kind"`
}

// Service 管理 VAPID 金鑰、訂閱，並把新的待我處理卡推出去。
type Service struct {
	DB *store.DB
	// Subject 是 VAPID 聯絡方式；空白用 XBOT_PUSH_SUBJECT 或 DefaultSubject。
	Subject string
	// Client 送推播用；nil 用有逾時的 http.Client。
	Client *http.Client
	Logger *slog.Logger

	mu sync.Mutex // 保護訂閱清單與金鑰的讀改寫
}

// New 建立推播服務。
func New(db *store.DB) *Service {
	s := &Service{DB: db, Subject: os.Getenv("XBOT_PUSH_SUBJECT")}
	if s.Subject == "" {
		s.Subject = DefaultSubject
	}
	return s
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: limits.PushSendTimeout}
}

// PublicKey 回傳 VAPID 公鑰（瀏覽器 pushManager.subscribe 的 applicationServerKey）；第一次呼叫時產生金鑰。
func (s *Service) PublicKey(ctx context.Context) (string, error) {
	v, err := s.keys(ctx)
	return v.Public, err
}

func (s *Service) keys(ctx context.Context) (vapid, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v vapid
	ok, err := s.DB.GetSettingInto(ctx, keyVAPID, &v)
	if err != nil {
		return v, err
	}
	if ok && v.Public != "" && v.Private != "" {
		return v, nil
	}
	if v.Private, v.Public, err = webpush.GenerateVAPIDKeys(); err != nil {
		return v, err
	}
	return v, s.DB.SetSetting(ctx, keyVAPID, v)
}

// List 回傳所有訂閱。
func (s *Service) List(ctx context.Context) ([]Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked(ctx)
}

func (s *Service) listLocked(ctx context.Context) ([]Subscription, error) {
	subs := []Subscription{}
	if _, err := s.DB.GetSettingInto(ctx, keySubs, &subs); err != nil {
		return nil, err
	}
	return subs, nil
}

// ErrTooMany 表示訂閱已經滿了。
var ErrTooMany = fmt.Errorf("最多 %d 個裝置接收推播，請先在其他裝置關掉", limits.PushMaxSubscriptions)

// Subscribe 新增或更新（同一個 endpoint）一個訂閱，回傳它的 id。
func (s *Service) Subscribe(ctx context.Context, sub Subscription) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs, err := s.listLocked(ctx)
	if err != nil {
		return "", err
	}
	for i := range subs {
		if subs[i].Endpoint == sub.Endpoint {
			sub.ID, sub.CreatedAt = subs[i].ID, subs[i].CreatedAt
			subs[i] = sub
			return sub.ID, s.DB.SetSetting(ctx, keySubs, subs)
		}
	}
	if len(subs) >= limits.PushMaxSubscriptions {
		return "", ErrTooMany
	}
	sub.ID = newID()
	sub.CreatedAt = time.Now().UnixMilli()
	subs = append(subs, sub)
	return sub.ID, s.DB.SetSetting(ctx, keySubs, subs)
}

// Unsubscribe 依 endpoint 或 id 移除訂閱；回傳有沒有移除。
func (s *Service) Unsubscribe(ctx context.Context, endpoint, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs, err := s.listLocked(ctx)
	if err != nil {
		return false, err
	}
	out := subs[:0]
	removed := false
	for _, x := range subs {
		if (endpoint != "" && x.Endpoint == endpoint) || (id != "" && x.ID == id) {
			removed = true
			continue
		}
		out = append(out, x)
	}
	if !removed {
		return false, nil
	}
	return true, s.DB.SetSetting(ctx, keySubs, out)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "push_" + hex.EncodeToString(b)
}

// ValidEndpoint 檢查推播網址：一定要是 https（推播服務都是），避免被拿來對內網發請求。
func ValidEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("推播網址要是 https 開頭（瀏覽器 pushManager 給的 endpoint）")
	}
	return nil
}

// EndpointHost 是給人看的推播服務名稱（例：fcm.googleapis.com）。
func EndpointHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// Result 是送一輪推播的結果。
type Result struct {
	Sent    int
	Failed  int
	Removed int // 推播服務說訂閱已失效（404／410），已經移除
}

// Send 把 msg 推給符合 pick 的訂閱（pick 為 nil 表示全部）。
func (s *Service) Send(ctx context.Context, msg Message, pick func(Subscription) bool) (Result, error) {
	subs, err := s.List(ctx)
	if err != nil {
		return Result{}, err
	}
	var targets []Subscription
	for _, x := range subs {
		if pick == nil || pick(x) {
			targets = append(targets, x)
		}
	}
	if len(targets) == 0 {
		return Result{}, nil
	}
	keys, err := s.keys(ctx)
	if err != nil {
		return Result{}, err
	}
	body, _ := json.Marshal(msg)
	subject := strings.TrimPrefix(s.Subject, "mailto:") // 函式庫自己會加 mailto:
	var (
		mu   sync.Mutex
		res  Result
		gone []string
		wg   sync.WaitGroup
		sem  = make(chan struct{}, limits.PushWorkers)
	)
	urgency := webpush.UrgencyHigh
	if msg.Kind == "done" || msg.Kind == "test" {
		urgency = webpush.UrgencyNormal
	}
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t Subscription) {
			defer func() { <-sem; wg.Done() }()
			sctx, cancel := context.WithTimeout(ctx, limits.PushSendTimeout)
			defer cancel()
			// 函式庫會直接改寫傳進去的 slice（加密時就地補位），每個裝置要用自己的一份。
			resp, err := webpush.SendNotificationWithContext(sctx, slices.Clone(body), &webpush.Subscription{
				Endpoint: t.Endpoint, Keys: webpush.Keys{P256dh: t.P256dh, Auth: t.Auth},
			}, &webpush.Options{
				HTTPClient: s.client(), Subscriber: subject, TTL: int(limits.PushTTL.Seconds()), Urgency: urgency,
				Topic: topicOf(msg.Tag), VAPIDPublicKey: keys.Public, VAPIDPrivateKey: keys.Private,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Failed++
				s.log().Warn("推播送不出去", "endpoint", EndpointHost(t.Endpoint), "err", err)
				return
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
				gone = append(gone, t.Endpoint)
				res.Failed++
			case resp.StatusCode >= 300:
				res.Failed++
				s.log().Warn("推播服務拒絕", "endpoint", EndpointHost(t.Endpoint), "status", resp.StatusCode)
			default:
				res.Sent++
			}
		}(t)
	}
	wg.Wait()
	for _, ep := range gone {
		if ok, err := s.Unsubscribe(ctx, ep, ""); err == nil && ok {
			res.Removed++
		}
	}
	return res, nil
}

// topicOf：同一張卡的推播互相取代（Topic 最多 32 個 URL-safe 字元）。
func topicOf(tag string) string {
	var b strings.Builder
	for _, r := range tag {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() == 32 {
			break
		}
	}
	return b.String()
}

var groupLabel = map[string]string{
	notify.GroupDecide: "要你決定",
	notify.GroupDone:   "工作完成",
}

// MessageFor 把 inbox.created 事件轉成推播；不需要推的回傳 false。
// 要你決定的卡推給所有裝置；工作完成只推給勾了「工作完成也通知」的裝置（由呼叫端過濾）。
func (s *Service) MessageFor(ctx context.Context, e events.Event) (Message, bool) {
	if e.Kind != notify.KindCreated {
		return Message{}, false
	}
	var p notify.Payload
	if json.Unmarshal(e.Payload, &p) != nil || p.ID == "" {
		return Message{}, false
	}
	label, ok := groupLabel[p.Group]
	if !ok {
		return Message{}, false
	}
	title := p.Title
	if p.Kind == "secret" {
		title = "有 Bot 需要你提供登入資料" // 密碼類卡片：推播不帶任何細節
	}
	if e.BotID != "" && p.Kind != "secret" {
		if b, err := s.DB.GetBot(ctx, e.BotID); err == nil && b.Name != "" && !strings.HasPrefix(title, b.Name) {
			title = b.Name + "：" + title
		}
	}
	if r := []rune(title); len(r) > 120 {
		title = string(r[:120]) + "…"
	}
	m := Message{Title: title, Body: label, URL: "/", Tag: p.ID, Kind: p.Group}
	if p.Group == notify.GroupDone && e.BotID != "" {
		m.URL = "/bots/" + url.PathEscape(e.BotID)
		if e.ThreadID != "" {
			m.URL += "/threads/" + url.PathEscape(e.ThreadID)
		}
	}
	return m, true
}

// Run 訂閱事件匯流排，把新的待我處理卡推出去，直到 ctx 結束。不會卡住匯流排（緩衝滿了就丟）。
func (s *Service) Run(ctx context.Context, bus *events.Bus) {
	sub := bus.Subscribe(func(e events.Event) bool { return e.Kind == notify.KindCreated }, limits.PushQueue)
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			s.handle(ctx, e)
		}
	}
}

func (s *Service) handle(ctx context.Context, e events.Event) {
	m, ok := s.MessageFor(ctx, e)
	if !ok {
		return
	}
	var pick func(Subscription) bool
	if m.Kind == notify.GroupDone {
		pick = func(x Subscription) bool { return x.NotifyDone }
	}
	if _, err := s.Send(ctx, m, pick); err != nil && ctx.Err() == nil {
		s.log().Warn("推播失敗", "err", err)
	}
}
