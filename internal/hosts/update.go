package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"

	"xbot/internal/limits"
	"xbot/internal/store"
)

// 電腦映像的版本（B13，B12 待決定事項「映像過期」）：make box 重建映像後，遠端主機上的映像就舊了。
// 主機清單標出 image_outdated，xbot.computers.update 一台一台主機地傳新映像並重建電腦（資料保留）。

// KindImageChanged：這台 XBot 的電腦映像換了（make box），主機與電腦要更新（payload：ImageChanged）。
const KindImageChanged = "host.image_changed"

// ImageChanged 是 host.image_changed 事件的內容。
type ImageChanged struct {
	Image    string `json:"image"`
	ImageID  string `json:"image_id"`
	Previous string `json:"previous,omitempty"`
}

type cachedRef struct {
	ref ImageRef
	at  time.Time
	// seen 是 watchImage 上次看到的映像 id（換了就發事件）。
	seen string
}

// cachedImage 是這台的電腦映像（快取 limits.HostImageCacheTTL，主機清單每次都要比對）。
func (s *Service) cachedImage(ctx context.Context) ImageRef {
	s.mu.Lock()
	c := s.img
	s.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < limits.HostImageCacheTTL {
		return c.ref
	}
	ref := s.image(ctx)
	s.mu.Lock()
	s.img.ref, s.img.at = ref, time.Now()
	s.mu.Unlock()
	return ref
}

// imageOutdated：主機上的映像（檢查或傳輸時記下的）和這台的不同。還沒檢查過時不算。
func imageOutdated(res Resources, local ImageRef) bool {
	if local.ID == "" || (res.ImageID == "" && res.ImageKey == "") {
		return false
	}
	if res.ImageID == local.ID || (local.Layers != "" && res.ImageKey == local.Layers) {
		return false
	}
	return true
}

// watchImage 在這台的電腦映像換了時發 host.image_changed（本機的健康檢查順便做）。
func (s *Service) watchImage() {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	ref := s.image(ctx)
	s.mu.Lock()
	prev := s.img.seen
	s.img.seen = ref.ID
	s.img.ref, s.img.at = ref, time.Now()
	s.mu.Unlock()
	if prev != "" && ref.ID != "" && prev != ref.ID {
		s.log.Info("電腦映像換了，主機上的電腦要更新", "image", ref.Name, "id", ref.ID)
		s.publish(KindImageChanged, ImageChanged{Image: ref.Name, ImageID: ref.ID, Previous: prev}, true)
		s.signal()
	}
}

// Signal 叫醒排隊等電腦的工作（例：有電腦休眠，空出名額，B13）。
func (s *Service) Signal() { s.signal() }

// ImageName 回傳電腦映像的名稱。
func (s *Service) ImageName() string { return s.o.Image }

// ErrImageLoad 表示電腦映像沒有傳到主機上。
var ErrImageLoad = errors.New("電腦映像沒有傳到主機上")

// EnsureImage 確保主機上的電腦映像和這台的一樣（「更新電腦」的第一步）：不一樣就傳過去並等傳完（進度看 host.image 事件）。
// 本機主機什麼都不用做。
func (s *Service) EnsureImage(ctx context.Context, id string) error {
	row, err := s.o.DB.GetHost(ctx, id)
	if err != nil {
		return err
	}
	if row.Kind == "local" {
		return nil
	}
	h := s.sshHost(id)
	if h == nil {
		return fmt.Errorf("%w：%s", ErrHostOffline, row.Name)
	}
	cctx, cancel := context.WithTimeout(ctx, limits.HostCheckTimeout)
	img := s.image(cctx)
	cancel()
	if img.ID == "" {
		return fmt.Errorf("這台 XBot 沒有電腦映像 %s（先執行 make box）", img.Name)
	}
	same := func() (bool, error) {
		cctx, cancel := context.WithTimeout(ctx, limits.HostCheckTimeout)
		defer cancel()
		got, err := h.docker.ImageInspect(cctx, img.Name)
		if err != nil {
			if isNoSuchImage(err) {
				return false, nil
			}
			return false, err
		}
		if !SameImage(got.InspectResponse, img) {
			return false, nil
		}
		if row, err := s.o.DB.GetHost(cctx, id); err == nil {
			var res Resources
			_ = json.Unmarshal(row.Resources, &res)
			if res.ImageID != got.ID || res.ImageKey != layersKey(got.InspectResponse) {
				res.ImageID, res.ImageKey = got.ID, layersKey(got.InspectResponse)
				row.Resources, _ = json.Marshal(res)
				_ = s.o.DB.UpdateHost(cctx, row)
			}
		}
		return true, nil
	}
	if ok, err := same(); err != nil || ok {
		return err
	}
	s.startLoad(id, h)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		s.mu.Lock()
		_, loading := s.loading[id]
		s.mu.Unlock()
		if !loading {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	if ok, err := same(); err != nil || ok {
		return err
	}
	msg := ""
	if row, err := s.o.DB.GetHost(ctx, id); err == nil && row.Status == store.HostFailed {
		msg = "：" + row.StatusError
	}
	return fmt.Errorf("%w（%s）%s", ErrImageLoad, row.Name, msg)
}

func isNoSuchImage(err error) bool {
	return cerrdefs.IsNotFound(err) || strings.Contains(strings.ToLower(err.Error()), "no such image")
}
