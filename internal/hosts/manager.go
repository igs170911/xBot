package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// 主機管理（B12，PRD §16）：遠端主機的連線、加入與檢查、健康狀態寫回 host.status、Bot 擺放與排隊（CL-1、CL-4、CL-6）。

// 事件種類（寫進 event 表、經 SSE 推給介面）。
const (
	// KindHostStatus：主機狀態變了（payload：StatusEvent）。
	KindHostStatus = "host.status"
	// KindHostImage：電腦映像傳輸進度（不寫資料庫；payload：ImageProgress）。
	KindHostImage = "host.image"
)

// DefaultHostSetting 是「新 Bot 放在哪台主機」的設定（setting 表，值是 host id 的 JSON 字串；空白＝這台電腦）。
const DefaultHostSetting = "hosts.default"

// StatusEvent 是 host.status 事件的內容。
type StatusEvent struct {
	HostID   string `json:"host_id"`
	Name     string `json:"name"`
	Status   string `json:"status" desc:"online、offline、updating、failed、removed"`
	Previous string `json:"previous,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ImageProgress 是電腦映像傳到主機的進度。
type ImageProgress struct {
	HostID  string `json:"host_id"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	Percent int    `json:"percent"`
	// Error 是傳輸失敗的原因（失敗時才有）。
	Error string `json:"error,omitempty"`
}

// Options 是主機管理的設定。
type Options struct {
	DB       *store.DB
	Registry *Registry
	Local    *Local
	DataDir  string
	// Image 是 Bot 電腦映像（要放到每台主機上）。
	Image string
	// LocalSocket 回傳 Bot 在本機的 product.sock（ipc.Server.Path）。
	LocalSocket func(botID string) string
	// Bus 收主機事件（可為 nil）。
	Bus    *events.Bus
	Logger *slog.Logger
	// Dial 建立 SSH 的 TCP 連線（測試用）；nil 用 net.Dialer。
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// RemoteDir、DockerSocket 覆寫遠端的資料目錄與 Docker socket（測試用；空白用預設）。
	RemoteDir, DockerSocket string
	// LocalDocker 是這台的 Docker API（讀與匯出電腦映像用）；nil 時用 Local.Docker()。測試可換掉。
	LocalDocker DockerAPI
}

// Service 管理所有主機。
type Service struct {
	o   Options
	log *slog.Logger
	ctx context.Context
	end context.CancelFunc
	wg  sync.WaitGroup

	mu       sync.Mutex
	img      cachedRef // 這台的電腦映像（List 用，短暫快取）
	ssh      map[string]*SSH
	watching map[string]chan struct{}
	status   map[string]string // 最後寫進 host.status 的值
	loading  map[string]*ImageProgress
	waiters  map[string][]string        // host → 排隊等電腦的 Bot（先來先開）
	starting map[string]map[string]bool // host → 已經放行、正在開機的 Bot（還沒算進 box.state）
	changed  chan struct{}              // 有主機狀態或名額變動時關閉，叫醒排隊的工作
}

// NewService 建立主機管理；用 Start 連上所有遠端主機。
func NewService(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, log: o.Logger, ctx: ctx, end: cancel,
		ssh: map[string]*SSH{}, watching: map[string]chan struct{}{}, status: map[string]string{},
		loading: map[string]*ImageProgress{}, waiters: map[string][]string{}, starting: map[string]map[string]bool{},
		changed: make(chan struct{})}
}

// Start 在背景連上資料庫裡所有遠端主機（斷線自動重連），並開始把健康狀態寫回 host.status。
func (s *Service) Start(ctx context.Context) error {
	rows, err := s.o.DB.ListHosts(ctx)
	if err != nil {
		return err
	}
	if s.o.Local != nil {
		s.watchLocal()
	}
	for _, row := range rows {
		if row.Kind != "ssh" {
			continue
		}
		if _, err := s.connect(row); err != nil {
			s.log.Warn("無法連線遠端主機", "host", row.Name, "err", err)
			s.writeStatus(row.ID, store.HostFailed, err.Error(), nil)
			continue
		}
		s.wg.Add(1)
		go func(id string) {
			defer s.wg.Done()
			s.ensureImageWhenOnline(id)
		}(row.ID)
	}
	return nil
}

// Close 關掉所有遠端主機的連線。
func (s *Service) Close() error {
	s.end()
	s.mu.Lock()
	all := make([]*SSH, 0, len(s.ssh))
	for _, h := range s.ssh {
		all = append(all, h)
	}
	s.mu.Unlock()
	var errs []error
	for _, h := range all {
		errs = append(errs, h.Close())
	}
	s.wg.Wait()
	return errors.Join(errs...)
}

// Identity 回傳 XBot 的 SSH 金鑰（第一次用時產生）。
func (s *Service) Identity() (Identity, error) { return EnsureIdentity(s.o.DataDir) }

func (s *Service) knownHostsPath(row store.Host) string {
	rel := row.KnownHosts
	if rel == "" {
		rel = filepath.ToSlash(filepath.Join("hosts", row.ID, "known_hosts"))
	}
	return filepath.Join(s.o.DataDir, filepath.FromSlash(rel))
}

// connect 依 host 列建立（或換掉）SSH 連線，加進登錄表並開始監看。
func (s *Service) connect(row store.Host) (*SSH, error) {
	id, err := s.Identity()
	if err != nil {
		return nil, err
	}
	user := row.SSHUser
	if user == "" {
		user = limits.HostSSHUser
	}
	port := row.Port
	if port == 0 {
		port = 22
	}
	h, err := NewSSH(SSHOptions{
		ID: row.ID, Addr: net.JoinHostPort(row.Address, strconv.Itoa(port)), User: user, Signer: id.Signer,
		KnownHosts: s.knownHostsPath(row), LocalSocket: s.o.LocalSocket, Dial: s.o.Dial,
		RemoteDir: s.o.RemoteDir, DockerSocket: s.o.DockerSocket,
		Logger: s.log.With("name", row.Name),
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	old := s.ssh[row.ID]
	s.ssh[row.ID] = h
	if stop, ok := s.watching[row.ID]; ok {
		close(stop)
	}
	stop := make(chan struct{})
	s.watching[row.ID] = stop
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	s.o.Registry.Add(h)
	s.wg.Add(1)
	go s.watch(row.ID, h, stop)
	return h, nil
}

func (s *Service) sshHost(id string) *SSH {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ssh[id]
}

// statusOf 把 SSH 連線狀態換成 host.status。
func (s *Service) statusOf(id string, h Health) (string, string) {
	s.mu.Lock()
	_, loading := s.loading[id]
	s.mu.Unlock()
	switch h.State {
	case SSHOnline:
		if loading {
			return store.HostUpdating, ""
		}
		return store.HostOnline, ""
	case SSHFailed:
		detail, fix := SSHErrorFix("", fmt.Errorf("%w：%s", ErrHostKeyMismatch, h.LastError))
		return store.HostFailed, detail + "。" + fix
	}
	if h.LastError == "" {
		return store.HostOffline, "正在連線"
	}
	return store.HostOffline, h.LastError
}

// watch 定期把 SSH 連線狀態寫回 host.status（只在變動時寫，並發 host.status 事件）。
func (s *Service) watch(id string, h *SSH, stop <-chan struct{}) {
	defer s.wg.Done()
	t := time.NewTicker(limits.HostWatchInterval)
	defer t.Stop()
	for {
		hl := h.Health()
		if hl.State == SSHClosed {
			return
		}
		st, msg := s.statusOf(id, hl)
		raw, _ := json.Marshal(hl)
		s.writeStatus(id, st, msg, raw)
		select {
		case <-stop:
			return
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// watchLocal 定期確認本機 Docker 還在。
func (s *Service) watchLocal() {
	id := s.o.Local.ID()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(limits.HostLocalPingInterval)
		defer t.Stop()
		for {
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			_, err := s.o.Local.Docker().Ping(ctx, client.PingOptions{})
			cancel()
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.writeStatus(id, store.HostOffline, "連不上這台的 Docker："+err.Error(), nil)
			} else {
				s.writeStatus(id, store.HostOnline, "", nil)
				s.watchImage()
			}
			select {
			case <-s.ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// writeStatus 寫 host.status；和上次不同時發 host.status 事件並叫醒排隊的工作。
func (s *Service) writeStatus(id, status, msg string, health json.RawMessage) {
	s.mu.Lock()
	prev, seen := s.status[id]
	s.status[id] = status
	s.mu.Unlock()
	ctx := context.Background()
	if seen && prev == status {
		if health != nil && status == store.HostOnline {
			// 只更新 health（往返時間、轉發數）和 last_seen_at，不發事件；每 30 秒寫一次就夠。
			if row, err := s.o.DB.GetHost(ctx, id); err == nil && time.Since(time.UnixMilli(row.UpdatedAt)) > 30*time.Second {
				_ = s.o.DB.SetHostHealth(ctx, id, status, msg, health)
			}
		}
		return
	}
	row, err := s.o.DB.GetHost(ctx, id)
	if err != nil {
		return
	}
	if !seen {
		prev = row.Status
	}
	if err := s.o.DB.SetHostHealth(ctx, id, status, msg, health); err != nil {
		s.log.Warn("寫入主機狀態失敗", "host", id, "err", err)
		return
	}
	if prev != status {
		s.log.Info("主機狀態", "host", row.Name, "status", status, "previous", prev, "detail", msg)
		s.publish(KindHostStatus, StatusEvent{HostID: id, Name: row.Name, Status: status, Previous: prev, Error: msg}, true)
	}
	s.signal()
}

func (s *Service) publish(kind string, payload any, persist bool) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	e := events.Event{Kind: kind, Payload: raw, At: time.Now().UnixMilli()}
	if persist {
		row, err := s.o.DB.AppendEvent(context.Background(), store.Event{Kind: kind, Payload: raw})
		if err == nil {
			e = events.FromStore(row)
		}
	}
	if s.o.Bus != nil {
		s.o.Bus.Publish(e)
	}
}

// signal 叫醒所有在等主機的工作。
func (s *Service) signal() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// ── 加入主機（CL-2）──────────────────────────────────

// Target 是要加入的主機位址。
type Target struct {
	Address string
	Port    int
	User    string
}

// ParseTarget 解析「[ssh://][user@]host[:port]」。
func ParseTarget(s string) (Target, error) {
	t := Target{Port: 22, User: limits.HostSSHUser}
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "ssh://"))
	if u, rest, ok := strings.Cut(s, "@"); ok {
		t.User, s = u, rest
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return t, fmt.Errorf("埠號不對：%q", p)
		}
		s, t.Port = h, n
	}
	s = strings.Trim(s, "[]")
	if s == "" || strings.ContainsAny(s, " /\\'\"") {
		return t, fmt.Errorf("主機位址不對：%q（填 Tailscale 名稱或 IP，例：cloud-01 或 100.101.7.23）", s)
	}
	t.Address = s
	return t, nil
}

// AddRequest 是加入主機的要求。
type AddRequest struct {
	Target
	Name       string
	MaxRunning int
	// SkipImage：不要把電腦映像傳過去（只檢查）。
	SkipImage bool
}

// AddResult 是加入主機的結果。
type AddResult struct {
	Host   View   `json:"host"`
	Report Report `json:"report"`
	// Added：主機已在清單裡（檢查沒過、新加入的主機不會留下）。
	Added bool `json:"added"`
	// Loading：電腦映像正在背景傳過去，傳完主機就會變成在線（進度看 host.image 事件）。
	Loading bool `json:"loading"`
}

// ErrNameTaken 表示同名主機已存在。
var ErrNameTaken = errors.New("已經有同名的主機，換一個名字")

// Add 加入（或重新檢查）一台遠端主機：連線、記下主機金鑰、自動檢查，通過就放進清單，需要時在背景把電腦映像傳過去。
// 同一個位址與埠重複加入時沿用原本那一列（可以重複執行）。檢查沒過時新的主機不會留下，回傳的 Report 說明原因。
func (s *Service) Add(ctx context.Context, req AddRequest) (AddResult, error) {
	if req.Address == "" {
		return AddResult{}, errors.New("要填主機位址")
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.User == "" {
		req.User = limits.HostSSHUser
	}
	req.Name = strings.TrimSpace(req.Name)
	rows, err := s.o.DB.ListHosts(ctx)
	if err != nil {
		return AddResult{}, err
	}
	var row store.Host
	created := true
	for _, r := range rows {
		if r.Kind == "ssh" && strings.EqualFold(r.Address, req.Address) && r.Port == req.Port {
			row, created = r, false
		}
	}
	if req.Name != "" {
		for _, r := range rows {
			if r.Name == req.Name && r.ID != row.ID {
				return AddResult{}, ErrNameTaken
			}
		}
	}
	if created {
		name := req.Name
		if name == "" {
			name = uniqueName(rows, defaultName(req.Address))
		}
		id := store.NewID()
		row = store.Host{ID: id, Name: name, Kind: "ssh", Address: req.Address, Port: req.Port, SSHUser: req.User,
			KeyRef: IdentityRef, KnownHosts: filepath.ToSlash(filepath.Join("hosts", id, "known_hosts")),
			Status: store.HostUpdating, MaxRunning: limits.HostDefaultMaxRunning}
		if req.MaxRunning > 0 {
			row.MaxRunning = req.MaxRunning
		}
		if row, err = s.o.DB.CreateHost(ctx, row); err != nil {
			return AddResult{}, err
		}
	} else {
		if req.Name != "" {
			row.Name = req.Name
		}
		if req.MaxRunning > 0 {
			row.MaxRunning = req.MaxRunning
		}
		row.SSHUser = req.User
		if err := s.o.DB.UpdateHost(ctx, row); err != nil {
			return AddResult{}, err
		}
	}
	h := s.sshHost(row.ID)
	if h == nil || h.Health().State == SSHFailed || h.Health().State == SSHClosed || h.o.User != row.SSHUser {
		if h, err = s.connect(row); err != nil {
			return AddResult{}, s.dropNew(ctx, created, row, err)
		}
	}
	report, err := s.inspect(ctx, h)
	if err != nil {
		return AddResult{}, s.dropNew(ctx, created, row, err)
	}
	res := AddResult{Report: report}
	if !report.OK {
		if created {
			if err := s.dropNew(ctx, true, row, nil); err != nil {
				return res, err
			}
			res.Host = View{Name: row.Name, Kind: "ssh", Address: row.Address, Port: row.Port, Status: store.HostFailed}
			return res, nil
		}
		s.writeStatus(row.ID, store.HostFailed, failSummary(report), nil)
	}
	if err := s.saveReport(ctx, row.ID, report); err != nil {
		return res, err
	}
	res.Added = true
	if report.OK && report.NeedImage && !req.SkipImage {
		res.Loading = s.startLoad(row.ID, h)
		if it := res.Report.Item(CheckImage); it != nil && res.Loading {
			it.Status = CheckRunning
		}
	}
	if report.OK && !res.Loading {
		s.mu.Lock()
		delete(s.status, row.ID) // 強制寫一次（之前可能是 failed）
		s.mu.Unlock()
		raw, _ := json.Marshal(h.Health())
		s.writeStatus(row.ID, store.HostOnline, "", raw)
	}
	res.Host, err = s.Get(ctx, row.ID)
	return res, err
}

// dropNew 把這次新建、但沒加入成功的主機清掉（連線、資料列、known_hosts）。
func (s *Service) dropNew(ctx context.Context, created bool, row store.Host, cause error) error {
	if !created {
		return cause
	}
	s.disconnect(row.ID)
	err := s.o.DB.DeleteHost(context.WithoutCancel(ctx), row.ID)
	_ = os.RemoveAll(filepath.Dir(s.knownHostsPath(row)))
	return errors.Join(cause, err)
}

func (s *Service) disconnect(id string) {
	s.mu.Lock()
	h := s.ssh[id]
	delete(s.ssh, id)
	if stop, ok := s.watching[id]; ok {
		close(stop)
		delete(s.watching, id)
	}
	delete(s.status, id)
	s.mu.Unlock()
	s.o.Registry.Remove(id)
	if h != nil {
		_ = h.Close()
	}
}

func failSummary(r Report) string {
	var parts []string
	for _, it := range r.Items {
		if it.Status == CheckFail {
			parts = append(parts, it.Label+"："+it.Detail)
		}
	}
	return strings.Join(parts, "；")
}

func defaultName(addr string) string {
	if ip := net.ParseIP(addr); ip != nil {
		return addr
	}
	name, _, _ := strings.Cut(addr, ".")
	return name
}

func uniqueName(rows []store.Host, base string) string {
	taken := map[string]bool{}
	for _, r := range rows {
		taken[r.Name] = true
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// image 讀這台 XBot 的電腦映像資訊。
func (s *Service) image(ctx context.Context) ImageRef {
	api := s.o.LocalDocker
	if api == nil && s.o.Local != nil {
		api = s.o.Local.Docker()
	}
	if api == nil {
		return ImageRef{Name: s.o.Image}
	}
	ref, err := LocalImage(ctx, api, s.o.Image)
	if err != nil {
		s.log.Warn("讀不到這台的電腦映像", "image", s.o.Image, "err", err)
	}
	return ref
}

// BoxImage 回傳這台的電腦映像（ID 空白＝還沒建，要 make box）；Docker 連不上時回錯誤。首次設定（ON-3）用。
func (s *Service) BoxImage(ctx context.Context) (ImageRef, error) {
	api := s.o.LocalDocker
	if api == nil && s.o.Local != nil {
		api = s.o.Local.Docker()
	}
	if api == nil {
		return ImageRef{Name: s.o.Image}, errors.New("這台沒有 Docker")
	}
	return LocalImage(ctx, api, s.o.Image)
}

func (s *Service) inspect(ctx context.Context, h *SSH) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.HostCheckTimeout)
	defer cancel()
	r := h.Inspect(ctx, s.image(ctx))
	s.mu.Lock()
	p := s.loading[h.ID()]
	s.mu.Unlock()
	if p != nil {
		if it := r.Item(CheckImage); it != nil {
			it.Status, it.Progress = CheckRunning, p.Percent
			it.Detail = fmt.Sprintf("%s · %s / %s", s.o.Image, GB(p.Done), GB(p.Total))
		}
	}
	return r, nil
}

func (s *Service) saveReport(ctx context.Context, id string, r Report) error {
	row, err := s.o.DB.GetHost(ctx, id)
	if err != nil {
		return err
	}
	if r.Resources.Arch != "" {
		row.Arch = r.Resources.Arch
	}
	if r.Resources.DockerVersion != "" {
		row.Resources, _ = json.Marshal(r.Resources)
	}
	return s.o.DB.UpdateHost(ctx, row)
}

// startLoad 在背景把電腦映像傳到主機（同一台同時只傳一份）。回傳是否開始（或已經在傳）。
func (s *Service) startLoad(id string, h *SSH) bool {
	s.mu.Lock()
	if _, busy := s.loading[id]; busy {
		s.mu.Unlock()
		return true
	}
	p := &ImageProgress{HostID: id}
	s.loading[id] = p
	s.mu.Unlock()
	s.writeStatus(id, store.HostUpdating, "正在傳電腦映像", nil)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := s.loadImage(id, h, p)
		s.mu.Lock()
		delete(s.loading, id)
		s.mu.Unlock()
		if err != nil {
			s.log.Warn("電腦映像傳輸失敗", "host", id, "err", err)
			s.publish(KindHostImage, ImageProgress{HostID: id, Error: err.Error()}, false)
			if s.ctx.Err() == nil {
				s.writeStatus(id, store.HostFailed, "電腦映像沒有傳完："+err.Error()+"。在主機清單按「重新檢查」再傳一次", nil)
			}
			return
		}
		ctx := context.Background()
		if row, err := s.o.DB.GetHost(ctx, id); err == nil {
			var res Resources
			_ = json.Unmarshal(row.Resources, &res)
			img := s.image(ctx)
			res.ImageID, res.ImageKey = img.ID, img.Layers
			row.Resources, _ = json.Marshal(res)
			_ = s.o.DB.UpdateHost(ctx, row)
		}
		raw, _ := json.Marshal(h.Health())
		s.writeStatus(id, store.HostOnline, "", raw)
	}()
	return true
}

func (s *Service) loadImage(id string, h *SSH, p *ImageProgress) error {
	ctx, cancel := context.WithTimeout(s.ctx, limits.HostImageLoadTimeout)
	defer cancel()
	src := s.o.LocalDocker
	if src == nil && s.o.Local != nil {
		src = s.o.Local.Docker()
	}
	saver, ok := src.(ImageSaver)
	if !ok {
		return errors.New("這台的 Docker 不能匯出映像")
	}
	img := s.image(ctx)
	if img.ID == "" {
		return fmt.Errorf("這台 XBot 沒有電腦映像 %s（先執行 make box）", img.Name)
	}
	return LoadImage(ctx, saver, h.docker, img, func(done, total int64) {
		pct := 0
		if total > 0 {
			pct = int(min(done*100/total, 100))
			if done < total {
				pct = min(pct, 99)
			}
		}
		s.mu.Lock()
		p.Done, p.Total, p.Percent = done, max(total, done), pct
		snap := *p
		s.mu.Unlock()
		s.publish(KindHostImage, snap, false)
	})
}

// ensureImageWhenOnline：服務重新啟動後，主機第一次連上時若完全沒有電腦映像（例：上次傳到一半），自動再傳一次。
// 映像版本不同（make box 重建過）不會自動傳，要在主機清單按「重新檢查」。
func (s *Service) ensureImageWhenOnline(id string) {
	h := s.sshHost(id)
	if h == nil {
		return
	}
	if err := h.WaitOnline(s.ctx); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, limits.HostCheckTimeout)
	defer cancel()
	img := s.image(ctx)
	if img.ID == "" {
		return
	}
	if _, err := h.docker.ImageInspect(ctx, img.Name); err != nil && strings.Contains(strings.ToLower(err.Error()), "no such image") {
		s.startLoad(id, h)
	}
}

// ── 檢查、修改、移除 ─────────────────────────────────

// Check 重新檢查一台主機並更新資源；離線或連線已停止（主機金鑰不符除外）時順便重新連線。
// fix 為 true 時，主機上的電腦映像和這台不同就傳過去（「更新電腦」）。
func (s *Service) Check(ctx context.Context, id string, fix bool) (AddResult, error) {
	row, err := s.o.DB.GetHost(ctx, id)
	if err != nil {
		return AddResult{}, err
	}
	if row.Kind == "local" {
		r := s.inspectLocal(ctx)
		if err := s.saveReport(ctx, id, r); err != nil {
			return AddResult{}, err
		}
		v, err := s.Get(ctx, id)
		return AddResult{Host: v, Report: r, Added: true}, err
	}
	h := s.sshHost(id)
	if h == nil || h.Health().State == SSHClosed {
		if h, err = s.connect(row); err != nil {
			return AddResult{}, err
		}
	}
	report, err := s.inspect(ctx, h)
	if err != nil {
		return AddResult{}, err
	}
	if err := s.saveReport(ctx, id, report); err != nil {
		return AddResult{}, err
	}
	res := AddResult{Report: report, Added: true}
	if report.OK && report.NeedImage && fix {
		res.Loading = s.startLoad(id, h)
		if it := res.Report.Item(CheckImage); it != nil {
			it.Status = CheckRunning
		}
	}
	if !report.OK && report.Item(CheckSSH).Status == CheckOK {
		s.writeStatus(id, store.HostFailed, failSummary(report), nil)
	} else if report.OK && !res.Loading && h.Health().State == SSHOnline {
		s.mu.Lock()
		_, loading := s.loading[id]
		s.mu.Unlock()
		if !loading {
			s.writeStatus(id, store.HostOnline, "", nil)
		}
	}
	res.Host, err = s.Get(ctx, id)
	return res, err
}

func (s *Service) inspectLocal(ctx context.Context) (r Report) {
	r = newReport()
	defer r.finish()
	r.Items = slices.DeleteFunc(r.Items, func(it CheckItem) bool {
		return it.ID == CheckSSH || it.ID == CheckForwarding || it.ID == CheckNetwork
	})
	if s.o.Local == nil {
		r.set(CheckDocker, CheckFail, "沒有本機 Docker", "")
		return r
	}
	info, err := s.o.Local.Docker().Info(ctx, client.InfoOptions{})
	if err != nil {
		r.set(CheckDocker, CheckFail, "連不上這台的 Docker："+err.Error(), "執行 make doctor 看怎麼修")
		return r
	}
	in := info.Info
	r.Resources = Resources{CPUs: in.NCPU, MemBytes: in.MemTotal, DockerVersion: in.ServerVersion, OS: in.OperatingSystem,
		Arch: NormalizeArch(in.Architecture), Hostname: in.Name}
	r.set(CheckDocker, CheckOK, in.ServerVersion+" · 可以使用", "")
	r.set(CheckResources, CheckOK, fmt.Sprintf("%d 核 · %s", in.NCPU, GB(in.MemTotal)), "")
	r.set(CheckArch, CheckOK, r.Resources.Arch, "")
	if img := s.image(ctx); img.ID == "" {
		r.set(CheckImage, CheckFail, "沒有電腦映像 "+img.Name, "執行 make box")
	} else {
		r.Resources.ImageID, r.Resources.ImageKey = img.ID, img.Layers
		r.set(CheckImage, CheckOK, img.Name, "")
	}
	return r
}

// SetLimits 改主機名稱或同時開機上限（0＝不限）。
func (s *Service) SetLimits(ctx context.Context, id string, name *string, maxRunning *int) (View, error) {
	row, err := s.o.DB.GetHost(ctx, id)
	if err != nil {
		return View{}, err
	}
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" {
			return View{}, errors.New("主機名稱不能是空的")
		}
		rows, err := s.o.DB.ListHosts(ctx)
		if err != nil {
			return View{}, err
		}
		for _, r := range rows {
			if r.Name == n && r.ID != id {
				return View{}, ErrNameTaken
			}
		}
		row.Name = n
	}
	if maxRunning != nil {
		if *maxRunning < 0 || *maxRunning > limits.HostMaxRunningCap {
			return View{}, fmt.Errorf("同時開機上限要在 0～%d 之間（0＝不限）", limits.HostMaxRunningCap)
		}
		row.MaxRunning = *maxRunning
	}
	if err := s.o.DB.UpdateHost(ctx, row); err != nil {
		return View{}, err
	}
	s.signal() // 上限變大時，排隊的工作可以開機了
	return s.Get(ctx, id)
}

// ErrHostInUse 表示主機上還有 Bot 的電腦。
var ErrHostInUse = errors.New("這台主機上還有 Bot 的電腦")

// ErrLocalHost 表示這台電腦（控制中心）不能移除。
var ErrLocalHost = errors.New("這台電腦是控制中心，不能移除；要換控制中心請把 XBot 服務裝到另一台（CL-8）")

// RemovePlan 是移除主機前要知道的事。
type RemovePlan struct {
	Host View
	// Boxes 是主機上還有電腦的 Bot 名稱（有的話不能移除，要先搬走或刪除）。
	Boxes []string
}

// PlanRemove 檢查能不能移除。
func (s *Service) PlanRemove(ctx context.Context, id string) (RemovePlan, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return RemovePlan{}, err
	}
	p := RemovePlan{Host: v}
	if v.Kind == "local" {
		return p, ErrLocalHost
	}
	boxes, err := s.o.DB.ListBoxes(ctx, id)
	if err != nil {
		return p, err
	}
	for _, b := range boxes {
		name := b.BotID
		if bot, err := s.o.DB.GetBot(ctx, b.BotID); err == nil {
			name = bot.Name
		}
		p.Boxes = append(p.Boxes, name)
	}
	if len(p.Boxes) > 0 {
		return p, fmt.Errorf("%w：%s。先把它們搬到別台或刪掉電腦", ErrHostInUse, strings.Join(p.Boxes, "、"))
	}
	return p, nil
}

// Remove 移除一台遠端主機：斷線、刪掉資料列與主機金鑰紀錄。放在這台、還沒有電腦的 Bot 改回自動擺放。
// 不會動遠端主機上的任何東西（xbot 帳號與 Docker 留著，要清掉請自己在主機上處理）。
func (s *Service) Remove(ctx context.Context, id string) (RemovePlan, error) {
	p, err := s.PlanRemove(ctx, id)
	if err != nil {
		return p, err
	}
	row, err := s.o.DB.GetHost(ctx, id)
	if err != nil {
		return p, err
	}
	s.disconnect(id)
	if err := s.o.DB.DeleteHost(ctx, id); err != nil {
		return p, err
	}
	_ = os.RemoveAll(filepath.Dir(s.knownHostsPath(row)))
	var def string
	if _, err := s.o.DB.GetSettingInto(ctx, DefaultHostSetting, &def); err == nil && def == id {
		_ = s.o.DB.SetSetting(ctx, DefaultHostSetting, "")
	}
	s.publish(KindHostStatus, StatusEvent{HostID: id, Name: row.Name, Status: "removed", Previous: row.Status}, true)
	s.mu.Lock()
	delete(s.waiters, id)
	delete(s.starting, id)
	s.mu.Unlock()
	s.signal()
	return p, nil
}

// SetDefault 設定新 Bot 預設放哪台主機（空白＝這台電腦）。
func (s *Service) SetDefault(ctx context.Context, id string) error {
	if id != "" {
		if _, err := s.o.DB.GetHost(ctx, id); err != nil {
			return err
		}
	}
	return s.o.DB.SetSetting(ctx, DefaultHostSetting, id)
}

func (s *Service) defaultHost(ctx context.Context) string {
	var id string
	if _, err := s.o.DB.GetSettingInto(ctx, DefaultHostSetting, &id); err != nil || id == "" {
		return s.o.Registry.Default()
	}
	if _, err := s.o.DB.GetHost(ctx, id); err != nil {
		return s.o.Registry.Default()
	}
	return id
}

// ── 清單 ────────────────────────────────────────────

// BotRef 是放在某台主機上的 Bot。
type BotRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Shape string `json:"shape"`
	// State 是電腦狀態（none＝還沒有電腦）。
	State   string `json:"state"`
	Running bool   `json:"running"`
}

// View 是主機清單的一列（J06）。
type View struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind" desc:"local（這台電腦）或 ssh（遠端主機）"`
	Address     string `json:"address"`
	Port        int    `json:"port,omitempty"`
	User        string `json:"user,omitempty"`
	Status      string `json:"status" desc:"online、offline、updating、failed、unknown"`
	StatusError string `json:"status_error,omitempty"`
	Arch        string `json:"arch"`
	// MaxRunning 是同時開機上限（0＝不限）；Running 是目前開機中的電腦。
	MaxRunning    int         `json:"max_running"`
	Running       int         `json:"running"`
	Resources     Resources   `json:"resources"`
	Health        *HealthView `json:"health,omitempty" desc:"遠端主機的 SSH 連線狀態"`
	LastSeenAt    int64       `json:"last_seen_at"`
	ControlCenter bool        `json:"control_center" desc:"XBot 服務就在這台（不能移除）"`
	Default       bool        `json:"default" desc:"新 Bot 預設放這台"`
	Bots          []BotRef    `json:"bots"`
	// Queued 是在這台排隊等電腦的 Bot 數（主機離線或滿了）。
	Queued int            `json:"queued"`
	Image  *ImageProgress `json:"image,omitempty" desc:"電腦映像傳輸中的進度"`
	// ImageOutdated：這台遠端主機上的電腦映像不是這台 XBot 目前的版本（make box 重建過），要「更新電腦」（B13）。
	ImageOutdated bool  `json:"image_outdated" desc:"主機上的電腦映像比這台 XBot 的舊，要更新電腦"`
	CreatedAt     int64 `json:"created_at"`
}

// HealthView 是對外的 SSH 連線狀態（Health 的 JSON 友善版本，時間是毫秒）。
type HealthView struct {
	State           string `json:"state" desc:"connecting、online、reconnecting、failed、closed"`
	Since           int64  `json:"since"`
	LastError       string `json:"last_error,omitempty"`
	Connects        int    `json:"connects"`
	RTTMillis       int64  `json:"rtt_ms"`
	ReverseForwards int    `json:"reverse_forwards"`
	LocalForwards   int    `json:"local_forwards"`
}

// List 列出所有主機與上面的 Bot。
func (s *Service) List(ctx context.Context) ([]View, error) {
	rows, err := s.o.DB.ListHosts(ctx)
	if err != nil {
		return nil, err
	}
	bots, err := s.o.DB.ListBots(ctx, false)
	if err != nil {
		return nil, err
	}
	boxes, err := s.o.DB.ListBoxes(ctx, "")
	if err != nil {
		return nil, err
	}
	boxOf := map[string]store.Box{}
	for _, b := range boxes {
		boxOf[b.BotID] = b
	}
	def := s.defaultHost(ctx)
	local := s.cachedImage(ctx)
	out := make([]View, 0, len(rows))
	for _, row := range rows {
		v := s.view(row, def)
		v.ImageOutdated = row.Kind != "local" && imageOutdated(v.Resources, local)
		for _, b := range bots {
			box, has := boxOf[b.ID]
			at := b.HostID
			if has && box.HostID != "" {
				at = box.HostID
			}
			if at != row.ID {
				continue
			}
			ref := BotRef{ID: b.ID, Name: b.Name, Color: b.Color, State: "none"}
			var av struct {
				Shape string `json:"shape"`
			}
			_ = json.Unmarshal(b.Avatar, &av)
			ref.Shape = av.Shape
			if has {
				ref.State = box.State
				ref.Running = box.State == "running" || box.State == "creating"
			}
			if ref.Running {
				v.Running++
			}
			v.Bots = append(v.Bots, ref)
		}
		out = append(out, v)
	}
	return out, nil
}

// Get 讀一台主機。
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	all, err := s.List(ctx)
	if err != nil {
		return View{}, err
	}
	for _, v := range all {
		if v.ID == id {
			return v, nil
		}
	}
	return View{}, store.ErrNotFound
}

func (s *Service) view(row store.Host, def string) View {
	v := View{ID: row.ID, Name: row.Name, Kind: row.Kind, Address: row.Address, Port: row.Port, User: row.SSHUser,
		Status: row.Status, StatusError: row.StatusError, Arch: row.Arch, MaxRunning: row.MaxRunning, LastSeenAt: row.LastSeenAt,
		ControlCenter: row.Kind == "local", Default: row.ID == def, Bots: []BotRef{}, CreatedAt: row.CreatedAt}
	_ = json.Unmarshal(row.Resources, &v.Resources)
	if h := s.sshHost(row.ID); h != nil {
		hl := h.Health()
		v.Health = &HealthView{State: hl.State, Since: hl.Since.UnixMilli(), LastError: hl.LastError, Connects: hl.Connects,
			RTTMillis: hl.RTT.Milliseconds(), ReverseForwards: hl.ReverseForwards, LocalForwards: hl.LocalForwards}
	}
	s.mu.Lock()
	if p := s.loading[row.ID]; p != nil {
		snap := *p
		v.Image = &snap
	}
	v.Queued = len(s.waiters[row.ID])
	s.mu.Unlock()
	return v
}

// ── 擺放與排隊（CL-1、CL-4、CL-6）───────────────────

// Wait 說明工作為什麼還在等電腦。
type Wait struct {
	// Reason：host_offline（主機離線，恢復後自動繼續）或 host_full（主機滿了，等電腦空出來）。
	Reason   string `json:"reason"`
	HostID   string `json:"host_id"`
	HostName string `json:"host_name"`
	// Position 是排第幾位（host_full 才有）。
	Position   int `json:"position,omitempty"`
	Running    int `json:"running,omitempty"`
	MaxRunning int `json:"max_running,omitempty"`
}

// 排隊原因。
const (
	WaitHostOffline = "host_offline"
	WaitHostFull    = "host_full"
)

// Place 決定 Bot 的電腦放在哪台主機：已經有電腦就是那台；Bot 指定了主機就用它；
// 都沒有時自動擺放（CL-4）：預設主機有空位就放那裡，否則放到有空位、在線、開機最少的主機；都滿了放預設主機排隊。
// 自動擺放的結果會記在 bot.host_id。
func (s *Service) Place(ctx context.Context, botID string) (string, error) {
	if box, err := s.o.DB.GetBox(ctx, botID); err == nil && box.HostID != "" {
		return box.HostID, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	bot, err := s.o.DB.GetBot(ctx, botID)
	if err != nil {
		return "", err
	}
	if bot.HostID != "" {
		return bot.HostID, nil
	}
	rows, err := s.o.DB.ListHosts(ctx)
	if err != nil {
		return "", err
	}
	loads, err := s.o.DB.HostLoads(ctx)
	if err != nil {
		return "", err
	}
	def := s.defaultHost(ctx)
	free := func(r store.Host) bool {
		if r.Kind != "local" && r.Status != store.HostOnline {
			return false
		}
		return r.MaxRunning == 0 || loads[r.ID].Running+s.startingCount(r.ID, botID) < r.MaxRunning
	}
	pick := def
	if i := slices.IndexFunc(rows, func(r store.Host) bool { return r.ID == def }); i < 0 || !free(rows[i]) {
		best := -1
		for i, r := range rows {
			if free(r) && (best < 0 || loads[r.ID].Running < loads[rows[best].ID].Running) {
				best = i
			}
		}
		if best >= 0 {
			pick = rows[best].ID
		}
	}
	if err := s.o.DB.SetBotHost(ctx, botID, pick); err != nil {
		return "", err
	}
	return pick, nil
}

func (s *Service) startingCount(hostID, except string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id := range s.starting[hostID] {
		if id != except {
			n++
		}
	}
	return n
}

// Admit 在 Bot 開機前呼叫（runtime）：主機離線或滿了就排隊等（report 收到原因，原因或名次變了才再呼叫），
// 可以開機時回傳 release —— 開機完成（或放棄）後一定要呼叫，讓名額改由 box.state 計算。
// ctx 被取消（停止工作）時回傳 ctx 的錯誤。這台電腦的 Docker 沒開時不排隊（照舊回報開不了機）。
func (s *Service) Admit(ctx context.Context, botID string, report func(Wait)) (func(), error) {
	var last Wait
	queued := ""
	leave := func() {
		if queued == "" {
			return
		}
		s.mu.Lock()
		s.waiters[queued] = slices.DeleteFunc(s.waiters[queued], func(id string) bool { return id == botID })
		s.mu.Unlock()
		queued = ""
		s.signal()
	}
	defer leave()
	for {
		s.mu.Lock()
		changed := s.changed
		s.mu.Unlock()
		hostID, err := s.Place(ctx, botID)
		if err != nil {
			return nil, err
		}
		row, err := s.o.DB.GetHost(ctx, hostID)
		if err != nil {
			return nil, err
		}
		if queued != "" && queued != hostID {
			leave()
		}
		w, ok, err := s.tryAdmit(ctx, row, botID, &queued)
		if err != nil {
			return nil, err
		}
		if ok {
			return func() {
				s.mu.Lock()
				delete(s.starting[row.ID], botID)
				s.mu.Unlock()
				s.signal()
			}, nil
		}
		if w != last && report != nil {
			report(w)
		}
		last = w
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("停止排隊：%w", ctx.Err())
		case <-changed:
		case <-time.After(limits.HostQueuePoll):
		}
	}
}

// tryAdmit 看這台主機現在能不能讓 Bot 開機；不能時把 Bot 排進隊伍並回傳原因。
func (s *Service) tryAdmit(ctx context.Context, row store.Host, botID string, queued *string) (Wait, bool, error) {
	w := Wait{HostID: row.ID, HostName: row.Name}
	if row.Kind != "local" && row.Status != store.HostOnline {
		// 主機離線（CL-6）：排進隊伍，主機回來時照順序開機。
		s.mu.Lock()
		if !slices.Contains(s.waiters[row.ID], botID) {
			s.waiters[row.ID] = append(s.waiters[row.ID], botID)
		}
		s.mu.Unlock()
		*queued = row.ID
		w.Reason = WaitHostOffline
		return w, false, nil
	}
	running := 0
	alreadyRunning := false
	if row.MaxRunning > 0 {
		boxes, err := s.o.DB.ListBoxes(ctx, row.ID)
		if err != nil {
			return w, false, err
		}
		for _, b := range boxes {
			if b.State != "running" && b.State != "creating" {
				continue
			}
			if b.BotID == botID {
				alreadyRunning = true
				continue
			}
			running++
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.starting[row.ID] {
		if id != botID {
			running++
		}
	}
	q := s.waiters[row.ID]
	pos := slices.Index(q, botID)
	if alreadyRunning || row.MaxRunning == 0 || (running < row.MaxRunning && (pos == 0 || (pos < 0 && len(q) == 0))) {
		if pos >= 0 {
			s.waiters[row.ID] = slices.Delete(q, pos, pos+1)
		}
		*queued = ""
		if s.starting[row.ID] == nil {
			s.starting[row.ID] = map[string]bool{}
		}
		s.starting[row.ID][botID] = true
		return w, true, nil
	}
	if pos < 0 {
		s.waiters[row.ID] = append(q, botID)
		pos = len(q)
	}
	*queued = row.ID
	w.Reason, w.Position, w.Running, w.MaxRunning = WaitHostFull, pos+1, running, row.MaxRunning
	return w, false, nil
}
