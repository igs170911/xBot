package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// execInspector 是能看 exec 行程還在不在的 Docker API（hosts.DockerAPI 符合）。有的話，關掉 agent 後會確認它真的結束了。
type execInspector interface {
	ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error)
}

// markerEnv 是每個 agent 行程的記號（環境變數，它開的 MCP 子行程也帶著）：關掉後還沒結束時，在電腦裡照記號強制結束。
// Docker 回的 exec Pid 是主機上的編號，容器裡用不了。
const markerEnv = "XBOT_ACP_RUN"

// Exec 用 docker exec（不配 TTY，stdin／stdout 各自一條）在容器裡開 agent 行程。
// 行程的 stdout 就是 ACP；stderr 只留最後一段寫紀錄。關掉 Process 會關掉 stdin 與連線，adapter 讀到 EOF 就會結束；
// 過了 limits.ACPExitGrace 還在的（例：Gemini CLI 不理 EOF）照記號強制結束。
func Exec(ctx context.Context, api Execer, spec ExecSpec, a Agent) (Process, error) {
	if len(a.Command) == 0 {
		return nil, errors.New("沒有設定 agent 的指令")
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	marker := hex.EncodeToString(b)
	env := append([]string{"HOME=" + spec.Home, markerEnv + "=" + marker}, a.Env...)
	env = append(env, spec.Env...)
	created, err := api.ExecCreate(ctx, spec.ContainerID, client.ExecCreateOptions{
		User: spec.User, AttachStdin: true, AttachStdout: true, AttachStderr: true,
		Env: env, WorkingDir: Workspace, Cmd: a.Command,
	})
	if err != nil {
		return nil, fmt.Errorf("無法在 Bot 的電腦裡開 %s：%w", a.Label, err)
	}
	// 連線建立後就和 ctx 無關：agent 行程要比開它的這一輪活得久。
	att, err := api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("無法連上 Bot 電腦裡的 %s：%w", a.Label, err)
	}
	p := newExecProcess(att.HijackedResponse)
	p.api, p.execID, p.containerID, p.user, p.marker = api, created.ID, spec.ContainerID, spec.User, marker
	return p, nil
}

type execProcess struct {
	resp   client.HijackedResponse
	stdout *io.PipeReader
	stderr *tail

	api                               Execer
	execID, containerID, user, marker string

	once sync.Once
}

func newExecProcess(resp client.HijackedResponse) *execProcess {
	pr, pw := io.Pipe()
	p := &execProcess{resp: resp, stdout: pr, stderr: &tail{max: limits.ACPStderrTailBytes}}
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
		_ = p.resp.CloseWrite()
		p.resp.Close()
		_ = p.stdout.Close()
		if in, ok := p.api.(execInspector); ok && p.marker != "" {
			go p.reap(in)
		}
	})
	return nil
}

// reap 等 agent 自己結束；過了 limits.ACPExitGrace 還在（不理 EOF 的 agent，或留下的子行程），就在 Bot 的電腦裡
// 把帶著同一個記號的行程強制結束（用開 agent 的同一個使用者，不用 root）。在背景做，不擋住呼叫的人。
func (p *execProcess) reap(in execInspector) {
	time.Sleep(limits.ACPExitGrace)
	ctx, cancel := context.WithTimeout(context.Background(), limits.ACPCallTimeout)
	defer cancel()
	if _, err := in.ExecInspect(ctx, p.execID, client.ExecInspectOptions{}); err != nil {
		return // 電腦已經停了
	}
	script := `for d in /proc/[0-9]*; do if tr '\0' '\n' < "$d/environ" 2>/dev/null | grep -qx "` + markerEnv + `=` + p.marker + `"; then kill -KILL "${d#/proc/}" 2>/dev/null; fi; done`
	created, err := p.api.ExecCreate(ctx, p.containerID, client.ExecCreateOptions{User: p.user, Cmd: []string{"sh", "-c", script}})
	if err != nil {
		return
	}
	if att, err := p.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{}); err == nil {
		_, _ = io.Copy(io.Discard, att.Reader)
		att.Close()
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
