package acp_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"slices"
	"testing"

	"github.com/moby/moby/client"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/acp/acptest"
)

// fakeExec 是只有 exec 的 Docker：行程就是假的 ACP agent，stdout 照 Docker 的多工格式送回，另外寫一段 stderr。
type fakeExec struct {
	agent   *acptest.Agent
	created []client.ExecCreateOptions
	target  string
}

func (f *fakeExec) ExecCreate(_ context.Context, id string, o client.ExecCreateOptions) (client.ExecCreateResult, error) {
	f.target = id
	f.created = append(f.created, o)
	return client.ExecCreateResult{ID: "e1"}, nil
}

func (f *fakeExec) ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
	ours, theirs := net.Pipe()
	p := f.agent.Start()
	go func() { _, _ = io.Copy(p, theirs); _ = p.Close() }()
	go func() {
		frame := func(stream byte, b []byte) error {
			hdr := []byte{stream, 0, 0, 0, 0, 0, 0, 0}
			binary.BigEndian.PutUint32(hdr[4:], uint32(len(b)))
			_, err := theirs.Write(append(hdr, b...))
			return err
		}
		_ = frame(2, []byte("adapter starting\n"))
		buf := make([]byte, 32<<10)
		for {
			n, err := p.Read(buf)
			if n > 0 && frame(1, buf[:n]) != nil {
				break
			}
			if err != nil {
				break
			}
		}
		theirs.Close()
	}()
	return client.ExecAttachResult{HijackedResponse: client.NewHijackedResponse(ours, "")}, nil
}

func TestExecProcess(t *testing.T) {
	f := &fakeExec{agent: acptest.New()}
	agent := acp.Agents[acp.EngineClaude]
	d := acp.New(acp.Options{Agent: agent, Launch: func(ctx context.Context, botID string, a acp.Agent) (acp.Process, error) {
		return acp.Exec(ctx, f, acp.ExecSpec{ContainerID: "c1", User: "1000:1000", Home: "/home/box", Env: []string{"X=1"}}, a)
	}})
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptHello}); err != nil {
		t.Fatal(err)
	}
	if got := text(collect(t, d.Events(s)), engine.EventText); got != "Hello" {
		t.Errorf("經 exec 的回覆 = %q", got)
	}
	o := f.created[0]
	if f.target != "c1" || o.User != "1000:1000" || o.WorkingDir != "/workspace" || !o.AttachStdin || !o.AttachStdout || o.TTY ||
		!slices.Equal(o.Cmd, []string{"claude-agent-acp"}) || !slices.Contains(o.Env, "HOME=/home/box") || !slices.Contains(o.Env, "X=1") ||
		!slices.Contains(o.Env, "DISABLE_AUTOUPDATER=1") {
		t.Errorf("exec 參數 = %+v", o)
	}
	d.Close("b")
	if e := until(t, d.Events(s), engine.EventError); !e.Fatal {
		t.Errorf("關掉後 = %+v", e)
	}
}
