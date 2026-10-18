package hosts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"xbot/internal/limits"
)

// ImageSaver 能把映像匯出成 tar（docker save）；*client.Client 符合。
type ImageSaver interface {
	ImageSave(ctx context.Context, imageIDs []string, opts ...client.ImageSaveOption) (client.ImageSaveResult, error)
}

// ImageLoader 能把 tar 載入成映像（docker load）；*client.Client 符合。
type ImageLoader interface {
	ImageLoad(ctx context.Context, input io.Reader, opts ...client.ImageLoadOption) (client.ImageLoadResult, error)
}

// LocalImage 讀這台 XBot 的電腦映像資訊（沒有這個映像時 ID 空白、不算錯誤）。
func LocalImage(ctx context.Context, api DockerAPI, name string) (ImageRef, error) {
	ref := ImageRef{Name: name}
	img, err := api.ImageInspect(ctx, name)
	if cerrdefs.IsNotFound(err) {
		return ref, nil
	}
	if err != nil {
		return ref, err
	}
	ref.ID, ref.Arch, ref.Layers = img.ID, NormalizeArch(img.Architecture), layersKey(img.InspectResponse)
	if img.Size > 0 {
		ref.Size = img.Size
	}
	return ref, nil
}

// layersKey 是映像內容的指紋（各層的 diff id）。不同 Docker 的映像 id 不一定一樣
// （新版 Docker 用 containerd 存映像時 id 是 manifest 的 digest），但內容相同時各層的 diff id 一定一樣。
func layersKey(img image.InspectResponse) string {
	if img.RootFS.Type == "" || len(img.RootFS.Layers) == 0 {
		return ""
	}
	h := sha256.Sum256([]byte(strings.Join(img.RootFS.Layers, ",")))
	return hex.EncodeToString(h[:])
}

// SameImage：主機上的映像和這台的是同一版（id 一樣，或各層內容一樣）。
func SameImage(got image.InspectResponse, want ImageRef) bool {
	return got.ID == want.ID || (want.Layers != "" && layersKey(got) == want.Layers)
}

// LoadImage 把 src 的映像經 docker save | docker load 串流到 dst（遠端主機的 Docker API 走 SSH），
// 期間每 limits.HostImageProgressEvery 回報一次已傳的位元組（total 是估計值：映像大小）。
// 不需要映像倉庫，也不會在任何一邊留下暫存檔。
func LoadImage(ctx context.Context, src ImageSaver, dst ImageLoader, img ImageRef, progress func(done, total int64)) error {
	rd, err := src.ImageSave(ctx, []string{img.Name})
	if err != nil {
		return fmt.Errorf("匯出電腦映像 %s 失敗：%w", img.Name, err)
	}
	defer rd.Close()
	cr := &countReader{r: rd}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(limits.HostImageProgressEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if progress != nil {
					progress(cr.n.Load(), img.Size)
				}
			}
		}
	}()
	res, err := dst.ImageLoad(ctx, cr, client.ImageLoadWithQuiet(true))
	if err == nil {
		err = readLoad(res)
	}
	close(stop)
	<-done
	if err != nil {
		return fmt.Errorf("把電腦映像傳到主機失敗：%w", err)
	}
	if progress != nil {
		progress(cr.n.Load(), cr.n.Load())
	}
	return nil
}

// readLoad 讀完 docker load 的回應（JSON 訊息串流），有錯誤訊息時回傳它。
func readLoad(res client.ImageLoadResult) error {
	defer res.Close()
	dec := json.NewDecoder(res)
	for {
		var m struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := dec.Decode(&m); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		if msg := strings.TrimSpace(m.ErrorDetail.Message + " " + m.Error); msg != "" {
			return errors.New(msg)
		}
	}
}

type countReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
