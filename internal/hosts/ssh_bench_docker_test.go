//go:build docker

package hosts_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"xbot/internal/box"
	"xbot/internal/hosts"
)

// TestSSHForwardBench 量 docs/spike-ssh-forward.md 的數字。只在 XBOT_B11_BENCH=1 時執行（要好幾分鐘）；
// XBOT_B11_BOTS 指定同時開幾台電腦（預設 10）。
func TestSSHForwardBench(t *testing.T) {
	if os.Getenv("XBOT_B11_BENCH") != "1" {
		t.Skip("設 XBOT_B11_BENCH=1 才跑實測")
	}
	n := 10
	if v, err := strconv.Atoi(os.Getenv("XBOT_B11_BOTS")); err == nil && v > 0 {
		n = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	s := newStack(ctx, t)

	// ── 開 n 台電腦（同一條 SSH 連線）────────────────────
	bots := make([]string, n)
	for i := range bots {
		bots[i] = s.newBot(ctx, t, fmt.Sprintf("實測%d", i)).ID
	}
	start := time.Now()
	errs := parallel(n, func(i int) error {
		if _, err := s.boxes.Start(ctx, bots[i]); err != nil {
			return err
		}
		_, err := s.boxes.WaitReady(ctx, bots[i])
		return err
	})
	if err := firstErr(errs); err != nil {
		t.Fatalf("開機：%v", err)
	}
	t.Logf("[開機] %d 台同時開機到全部就緒：%s", n, time.Since(start).Round(time.Millisecond))

	wk := make([]string, n)
	for i, id := range bots {
		k, err := s.keys.Issue(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		wk[i] = string(k)
	}

	// ── 模型請求延遲：本機 socket（沒有 SSH）vs 遠端 socket（經反向轉發）────
	const reqs = 50
	local := func(i int, reuse bool) []time.Duration {
		out, err := exec.CommandContext(ctx, "sh", "-c", curlScript(s.sockets.Path(bots[i]), wk[i], reqs, reuse)).CombinedOutput()
		if err != nil {
			t.Fatalf("本機 curl：%v %s", err, out)
		}
		return parseTimes(t, string(out))
	}
	remote := func(i int, reuse bool) []time.Duration {
		out, err := s.th.Shell(ctx, curlScript(s.host.IPCDir(bots[i])+"/product.sock", wk[i], reqs, reuse))
		if err != nil {
			t.Fatalf("遠端 curl：%v %s", err, out)
		}
		return parseTimes(t, out)
	}
	t.Logf("[模型請求] 本機 socket，每次新連線：%s", stats(local(0, false)))
	t.Logf("[模型請求] 本機 socket，沿用連線：%s", stats(local(0, true)))
	t.Logf("[模型請求] 遠端經 SSH，每次新連線：%s", stats(remote(0, false)))
	t.Logf("[模型請求] 遠端經 SSH，沿用連線：%s", stats(remote(0, true)))

	// ── noVNC：經本機轉發埠 vs 在遠端直接連 ─────────────
	st, err := s.boxes.Status(ctx, bots[0])
	if err != nil {
		t.Fatal(err)
	}
	vncRemote, err := s.th.Shell(ctx, fmt.Sprintf(`for i in $(seq %d); do curl -s -o /dev/null -w '%%{time_total}\n' http://127.0.0.1:%d/vnc.html; done`, reqs, st.NoVNCPort))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("[noVNC] 在遠端直接 GET /vnc.html：%s", stats(parseTimes(t, vncRemote)))
	t.Logf("[noVNC] 經 SSH 轉發 GET /vnc.html（每次新連線）：%s", stats(httpTimes(ctx, t, "http://"+st.NoVNCAddr+"/vnc.html", reqs)))
	t.Logf("[noVNC] 經 SSH 轉發 WebSocket 連上到收到 RFB 版本：%s", stats(rfbTimes(ctx, t, st.NoVNCAddr, 20)))
	if _, err := s.th.Exec(ctx, "docker", "exec", "-u", "root", box.ContainerName(bots[0]), "dd", "if=/dev/urandom", "of=/usr/share/novnc/big.bin", "bs=1M", "count=100"); err != nil {
		t.Fatal(err)
	}
	t.Logf("[noVNC] 經 SSH 轉發下載 100 MB：%s", throughput(ctx, t, "http://"+st.NoVNCAddr+"/big.bin"))

	// ── 加 20 ms 往返延遲（模擬 tailnet）─────────────────
	if _, err := s.th.Exec(ctx, "tc", "qdisc", "add", "dev", "eth0", "root", "netem", "delay", "20ms"); err != nil {
		t.Logf("無法加延遲（略過）：%v", err)
	} else {
		time.Sleep(11 * time.Second) // 等一次 keepalive
		t.Logf("[RTT+20ms] keepalive RTT：%s", s.host.Health().RTT.Round(time.Microsecond))
		t.Logf("[RTT+20ms][模型請求] 遠端經 SSH，每次新連線：%s", stats(remote(0, false)))
		t.Logf("[RTT+20ms][模型請求] 遠端經 SSH，沿用連線：%s", stats(remote(0, true)))
		t.Logf("[RTT+20ms][noVNC] GET /vnc.html：%s", stats(httpTimes(ctx, t, "http://"+st.NoVNCAddr+"/vnc.html", 20)))
		t.Logf("[RTT+20ms][noVNC] WebSocket 到 RFB 版本：%s", stats(rfbTimes(ctx, t, st.NoVNCAddr, 10)))
		t.Logf("[RTT+20ms][noVNC] 下載 100 MB：%s", throughput(ctx, t, "http://"+st.NoVNCAddr+"/big.bin"))
		var turns []time.Duration
		thread := ""
		for range 5 {
			var d time.Duration
			thread, d = s.chat(ctx, t, bots[0], thread, "回覆 OK")
			turns = append(turns, d)
		}
		t.Logf("[RTT+20ms][對話] 一輪（Hermes＋模型＋事件串流）：%s", stats(turns[1:]))
		if _, err := s.th.Exec(ctx, "tc", "qdisc", "del", "dev", "eth0", "root"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
	}
	var turns []time.Duration
	thread := ""
	for range 5 {
		var d time.Duration
		thread, d = s.chat(ctx, t, bots[0], thread, "回覆 OK")
		turns = append(turns, d)
	}
	t.Logf("[對話] 一輪（無額外延遲）：%s", stats(turns[1:]))

	// ── n 台同時 ─────────────────────────────────────
	var mu sync.Mutex
	var all, allReuse []time.Duration
	start = time.Now()
	parallel(n, func(i int) error {
		a := remote(i, false)
		b := remote(i, true)
		mu.Lock()
		all, allReuse = append(all, a...), append(allReuse, b...)
		mu.Unlock()
		return nil
	})
	t.Logf("[%d 台同時][模型請求] 每台 %d 次新連線＋%d 次沿用連線，共 %s：新連線 %s；沿用 %s", n, reqs, reqs, time.Since(start).Round(time.Millisecond), stats(all), stats(allReuse))
	var vnc []time.Duration
	errs = parallel(n, func(i int) error {
		st, err := s.boxes.Status(ctx, bots[i])
		if err != nil {
			return err
		}
		d := httpTimes(ctx, t, "http://"+st.NoVNCAddr+"/vnc.html", 20)
		mu.Lock()
		vnc = append(vnc, d...)
		mu.Unlock()
		return nil
	})
	if err := firstErr(errs); err != nil {
		t.Fatal(err)
	}
	t.Logf("[%d 台同時][noVNC] GET /vnc.html：%s", n, stats(vnc))
	var chats []time.Duration
	errs = parallel(n, func(i int) error {
		d, err := chatOnce(ctx, s, bots[i])
		mu.Lock()
		chats = append(chats, d)
		mu.Unlock()
		return err
	})
	if err := firstErr(errs); err != nil {
		t.Fatalf("同時對話：%v", err)
	}
	t.Logf("[%d 台同時][對話] 每台一輪：%s", n, stats(chats))

	// ── 通道數量上限 ─────────────────────────────────
	// 200 條同時進行的 product.sock 連線（每條 hold 2 秒的慢請求不好做，用 200 個並行 curl）。
	out, _ := s.th.Shell(ctx, fmt.Sprintf(`for i in $(seq 200); do curl -s -o /dev/null -w '%%{http_code}\n' --unix-socket %s -H 'Authorization: Bearer %s' -H 'Content-Type: application/json' -d '{"model":"fake-model","messages":[{"role":"user","content":"hi"}]}' http://x/internal/providers/openai/v1/chat/completions & done; wait`,
		s.host.IPCDir(bots[0])+"/product.sock", wk[0]))
	t.Logf("[上限] 200 個同時的 product.sock 請求：%s", countCodes(out))
	// 200 條同時開著的 noVNC WebSocket（經本機轉發埠，每條一個 SSH channel）。
	var wsOK, wsFail atomic.Int32
	var conns []*websocket.Conn
	var cmu sync.Mutex
	parallel(200, func(i int) error {
		st, _ := s.boxes.Status(ctx, bots[i%n])
		c, _, err := websocket.Dial(ctx, "ws://"+st.NoVNCAddr+"/websockify", &websocket.DialOptions{Subprotocols: []string{"binary"}})
		if err != nil {
			wsFail.Add(1)
			return nil
		}
		if _, _, err := c.Read(ctx); err != nil {
			wsFail.Add(1)
		} else {
			wsOK.Add(1)
		}
		cmu.Lock()
		conns = append(conns, c)
		cmu.Unlock()
		return nil
	})
	t.Logf("[上限] 200 條同時開著的 noVNC WebSocket：成功 %d、失敗 %d", wsOK.Load(), wsFail.Load())
	for _, c := range conns {
		c.Close(websocket.StatusNormalClosure, "")
	}
	// sshd 的 MaxSessions=10 只限制 session（執行指令），不限制轉發。
	var runOK, runFail atomic.Int32
	parallel(20, func(int) error {
		if _, err := s.host.Run(ctx, "sleep 2"); err != nil {
			runFail.Add(1)
		} else {
			runOK.Add(1)
		}
		return nil
	})
	t.Logf("[上限] 20 個同時的指令 session（MaxSessions=10）：成功 %d、失敗 %d", runOK.Load(), runFail.Load())
	if rss, err := s.th.Shell(ctx, `ps -o pid,rss,args | grep '[s]shd-session: xbot'`); err == nil {
		t.Logf("[資源] 遠端 sshd（這條連線）：%s", strings.Join(strings.Fields(rss), " "))
	}

	// ── 負載中斷線 ───────────────────────────────────
	connects := s.host.Health().Connects
	loop := fmt.Sprintf(`for i in $(seq 120); do curl -s -o /dev/null -w '%%{http_code} %%{time_total}\n' --max-time 30 --unix-socket %s -H 'Authorization: Bearer %s' -H 'Content-Type: application/json' -d '{"model":"fake-model","messages":[{"role":"user","content":"hi"}]}' http://x/internal/providers/openai/v1/chat/completions; sleep 0.05; done`,
		s.host.IPCDir(bots[0])+"/product.sock", wk[0])
	for _, how := range []string{"本機切斷 TCP", "遠端殺掉 sshd"} {
		res := make(chan string, 1)
		go func() { out, _ := s.th.Shell(ctx, loop); res <- out }()
		time.Sleep(2 * time.Second)
		if how == "本機切斷 TCP" {
			s.conns.killLast()
		} else if _, err := s.th.Exec(ctx, "pkill", "sshd"); err != nil {
			t.Fatal(err)
		}
		connects++
		took := s.waitConnects(t, connects, 2*time.Minute)
		out := <-res
		t.Logf("[重連][%s] 重新連上 %s；斷線期間 120 個請求（每 50 ms 一個）：%s；最慢 %s", how, took.Round(time.Millisecond), countCodes(out), maxTime(out))
	}

	// 主機整個凍結 20 秒（TCP 沒斷但沒有回應）：要靠 keepalive 發現。
	if _, err := exec.CommandContext(ctx, "docker", "pause", s.th.Container).CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	for s.host.Health().State == hosts.SSHOnline && time.Since(t0) < time.Minute {
		time.Sleep(50 * time.Millisecond)
	}
	detect := time.Since(t0)
	time.Sleep(time.Until(t0.Add(20 * time.Second)))
	if out, err := exec.CommandContext(ctx, "docker", "unpause", s.th.Container).CombinedOutput(); err != nil {
		t.Fatalf("unpause：%v %s", err, out)
	}
	t1 := time.Now()
	connects++
	s.waitConnects(t, connects, 2*time.Minute)
	t.Logf("[重連][主機凍結 20 秒] %s 後發現斷線；恢復後 %s 重新連上", detect.Round(time.Millisecond), time.Since(t1).Round(time.Millisecond))
	if _, err := chatOnce(ctx, s, bots[0]); err != nil {
		t.Fatalf("凍結恢復後對話：%v", err)
	}
	t.Logf("[健康] %+v", s.host.Health())
}

func curlScript(sock, key string, n int, reuse bool) string {
	body := `{"model":"fake-model","messages":[{"role":"user","content":"hi"}]}`
	args := fmt.Sprintf(`-s -H 'Authorization: Bearer %s' -H 'Content-Type: application/json' -d '%s' --unix-socket %s`, key, body, sock)
	url := "http://x/internal/providers/openai/v1/chat/completions"
	if reuse {
		// 一個 curl 依序送 n 個請求，沿用同一條連線（像 hermes_host.py 的 httpx 連線池）。
		return fmt.Sprintf(`curl %s -w '\nT %%{time_total}\n' '%s?i=[1-%d]' | grep '^T ' | cut -c3-`, args, url, n)
	}
	return fmt.Sprintf(`for i in $(seq %d); do curl %s -o /dev/null -w '%%{time_total}\n' %s; done`, n, args, url)
}

func parseTimes(t *testing.T, out string) []time.Duration {
	t.Helper()
	var d []time.Duration
	for _, f := range strings.Fields(out) {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			t.Fatalf("解析時間 %q：%v\n%s", f, err, out)
		}
		d = append(d, time.Duration(v*float64(time.Second)))
	}
	if len(d) == 0 {
		t.Fatalf("沒有結果：%q", out)
	}
	return d
}

func stats(d []time.Duration) string {
	s := slices.Clone(d)
	slices.Sort(s)
	var sum time.Duration
	for _, v := range s {
		sum += v
	}
	pick := func(p float64) time.Duration { return s[min(len(s)-1, int(p*float64(len(s))))] }
	r := func(v time.Duration) string { return fmt.Sprintf("%.2fms", float64(v)/float64(time.Millisecond)) }
	return fmt.Sprintf("n=%d 中位數 %s、p95 %s、最大 %s、平均 %s", len(s), r(pick(.5)), r(pick(.95)), r(s[len(s)-1]), r(sum/time.Duration(len(s))))
}

func httpTimes(ctx context.Context, t *testing.T, url string, n int) []time.Duration {
	cl := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 30 * time.Second}
	var d []time.Duration
	for range n {
		start := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		res, err := cl.Do(req)
		if err != nil {
			t.Errorf("GET %s：%v", url, err)
			continue
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		d = append(d, time.Since(start))
	}
	return d
}

func rfbTimes(ctx context.Context, t *testing.T, addr string, n int) []time.Duration {
	var d []time.Duration
	for range n {
		start := time.Now()
		c, _, err := websocket.Dial(ctx, "ws://"+addr+"/websockify", &websocket.DialOptions{Subprotocols: []string{"binary"}})
		if err != nil {
			t.Errorf("WebSocket：%v", err)
			continue
		}
		_, msg, err := c.Read(ctx)
		if err != nil || !strings.HasPrefix(string(msg), "RFB ") {
			t.Errorf("RFB：%q %v", msg, err)
		}
		d = append(d, time.Since(start))
		c.Close(websocket.StatusNormalClosure, "")
	}
	return d
}

func throughput(ctx context.Context, t *testing.T, url string) string {
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	n, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		t.Fatal(err)
	}
	d := time.Since(start)
	return fmt.Sprintf("%.1f MB／%s ＝ %.0f MB/s", float64(n)/1e6, d.Round(time.Millisecond), float64(n)/1e6/d.Seconds())
}

func chatOnce(ctx context.Context, s *stack, botID string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	start := time.Now()
	res, err := s.rt.Chat(ctx, botID, "", "回覆 OK")
	if err != nil {
		return 0, err
	}
	task, err := s.rt.Wait(ctx, res.TaskID)
	if err != nil {
		return 0, err
	}
	if task.Verdict != "answer" {
		return 0, fmt.Errorf("對話沒有正常完成：%+v", task)
	}
	return time.Since(start), nil
}

func countCodes(out string) string {
	codes := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			codes[f[0]]++
		}
	}
	keys := make([]string, 0, len(codes))
	for k := range codes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("HTTP %s × %d", k, codes[k]))
	}
	return strings.Join(parts, "、")
}

func maxTime(out string) string {
	var m float64
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			if v, err := strconv.ParseFloat(f[1], 64); err == nil && v > m {
				m = v
			}
		}
	}
	return fmt.Sprintf("%.0fms", m*1000)
}

func parallel(n int, f func(i int) error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { errs[i] = f(i) })
	}
	wg.Wait()
	return errs
}

func firstErr(errs []error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
