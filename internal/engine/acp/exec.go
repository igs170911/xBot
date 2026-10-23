package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"xbot/internal/limits"
)

// Execer 是開 agent 行程用到的 Docker API；hosts.DockerAPI 符合。
type Execer interface {
	ExecCreate(ctx context.Context, containerID string, options client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, options client.ExecAttachOptions) (client.ExecAttachResult, error)
	// 2026-10-22k (item 7b): ExecInspect 取得 PID 與清理子行程
	ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error)
}

// ExecSpec 是在容器裡開 agent 行程的方式。
type ExecSpec struct {
	ContainerID string
	// User 是 "uid:gid"（Bot 使用者，不用 root）。
	User string
	// Home 是 Bot 的家目錄（登入資料在這裡）。
	Home string
	// Env 是額外的環境變數（映像的 ENV 會沿用）。
	Env []string
}

// Exec 用 docker exec（不配 TTY，stdin／stdout 各自一條）在容器裡開 agent 行程。
// 行程的 stdout 就是 ACP；stderr 只留最後一段寫紀錄。關掉 Process 會關掉 stdin 與連線，adapter 讀到 EOF 就會結束。
// 2026-10-22k (item 7b): 使用 setsid 建立新的 process group，以便清理子行程。
func Exec(ctx context.Context, api Execer, spec ExecSpec, a Agent) (Process, error) {
	if len(a.Command) == 0 {
		return nil, errors.New("沒有設定 agent 的指令")
	}
	env := append([]string{"HOME=" + spec.Home}, a.Env...)
	env = append(env, spec.Env...)

	// 2026-10-22j (B1): setsid -w creates process group AND waits for child, keeping docker stream open
	wrappedCmd := append([]string{"setsid", "-w"}, a.Command...)

	created, err := api.ExecCreate(ctx, spec.ContainerID, client.ExecCreateOptions{
		User: spec.User, AttachStdin: true, AttachStdout: true, AttachStderr: true,
		Env: env, WorkingDir: Workspace, Cmd: wrappedCmd,
	})
	if err != nil {
		return nil, fmt.Errorf("無法在 Bot 的電腦裡開 %s：%w", a.Label, err)
	}
	// 連線建立後就和 ctx 無關：agent 行程要比開它的這一輪活得久。
	att, err := api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("無法連上 Bot 電腦裡的 %s：%w", a.Label, err)
	}
	return newExecProcess(api, spec.ContainerID, created.ID, att.HijackedResponse), nil
}

type execProcess struct {
	api         Execer
	containerID string
	execID      string
	resp        client.HijackedResponse
	stdout      *io.PipeReader
	stderr      *tail

	once sync.Once
}

// ContainerID 回傳容器 ID（B2.3, 2026-10-22）。
func (p *execProcess) ContainerID() string { return p.containerID }

// Execer 回傳 Docker API execer（B2.3, 2026-10-22）。
func (p *execProcess) Execer() Execer { return p.api }

func newExecProcess(api Execer, containerID, execID string, resp client.HijackedResponse) *execProcess {
	pr, pw := io.Pipe()
	p := &execProcess{
		api:         api,
		containerID: containerID,
		execID:      execID,
		resp:        resp,
		stdout:      pr,
		stderr:      &tail{max: limits.ACPStderrTailBytes},
	}
	go func() {
		_, err := stdcopy.StdCopy(pw, p.stderr, resp.Reader)
		if err == nil {
			err = io.EOF
		}
		pw.CloseWithError(err)
	}()
	return p
}

func (p *execProcess) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *execProcess) Write(b []byte) (int, error) { return p.resp.Conn.Write(b) }

func (p *execProcess) Close() error {
	p.once.Do(func() {
		// 2026-10-22k (item 7b): 清理子行程
		p.killProcessGroup()
		_ = p.resp.CloseWrite()
		p.resp.Close()
		_ = p.stdout.Close()
	})
	return nil
}

// killProcessGroup 終止這個 exec 及其子行程（2026-10-22k item 7b）
func (p *execProcess) killProcessGroup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 取得 exec 的 PID
	info, err := p.api.ExecInspect(ctx, p.execID, client.ExecInspectOptions{})
	if err != nil {
		// Exec 可能已經結束，這不是錯誤
		return
	}

	if info.PID == 0 || !info.Running {
		return // 已經結束
	}

	// 使用 Docker exec 發送 SIGTERM 到整個 process group
	// 因為我們用 setsid 啟動，PID 就是 session ID
	// kill -- -PID 會 kill 整個 process group
	killCmd := []string{"sh", "-c", fmt.Sprintf("kill -TERM -- -%d 2>/dev/null || true", info.PID)}
	killExec, err := p.api.ExecCreate(ctx, p.containerID, client.ExecCreateOptions{
		Cmd: killCmd,
	})
	if err == nil {
		killAtt, err := p.api.ExecAttach(ctx, killExec.ID, client.ExecAttachOptions{})
		if err == nil {
			killAtt.Close()
		}
	}

	// E4: 等 3 秒讓行程有時間清理
	time.Sleep(3 * time.Second)

	// 如果還在跑，發 SIGKILL
	info2, err := p.api.ExecInspect(ctx, p.execID, client.ExecInspectOptions{})
	if err == nil && info2.Running {
		killCmd := []string{"sh", "-c", fmt.Sprintf("kill -KILL -- -%d 2>/dev/null || true", info.PID)}
		killExec, err := p.api.ExecCreate(ctx, p.containerID, client.ExecCreateOptions{
			Cmd: killCmd,
		})
		if err == nil {
			killAtt, err := p.api.ExecAttach(ctx, killExec.ID, client.ExecAttachOptions{})
			if err == nil {
				killAtt.Close()
			}
		}
	}
}

func (p *execProcess) StderrTail() string { return p.stderr.String() }

// tail 只保留最後 max bytes。
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append([]byte(nil), t.buf[over:]...)
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
