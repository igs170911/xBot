package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"xbot/internal/screen"
)

// fakeComputers 假裝是服務：screens 回固定清單，其他操作記下呼叫了誰。
type fakeComputers struct {
	screens []screen.Screen
	fail    map[string]bool // 這些 bot_id 的操作失敗
	calls   []string        // "op bot_id"
}

func (f *fakeComputers) call(_ context.Context, name string, in, out any) error {
	if name == "xbot.computers.screens" {
		raw, _ := json.Marshal(screen.ScreensOut{Screens: f.screens})
		return json.Unmarshal(raw, out)
	}
	id := in.(map[string]any)["bot_id"].(string)
	f.calls = append(f.calls, name+" "+id)
	if f.fail[id] {
		return errors.New("主機連不上")
	}
	return nil
}

func sampleComputers() *fakeComputers {
	return &fakeComputers{screens: []screen.Screen{
		{BotID: "a", Name: "水哥哥", Box: "running"},
		{BotID: "b", Name: "島灰", Box: "sleeping"},
		{BotID: "c", Name: "研究員", Box: "running", Working: true},
		{BotID: "d", Name: "文案", Box: "none"},
		{BotID: "e", Name: "驗證員", Box: "running"},
	}}
}

func TestSleepAllOnlyRunningAndIdle(t *testing.T) {
	f := sampleComputers()
	var out strings.Builder
	if err := sleepAll(context.Background(), f.call, &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"xbot.computers.sleep a", "xbot.computers.sleep e"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	for _, s := range []string{"研究員：正在工作，跳過", "關機 2 台", "跳過 1 台"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("輸出沒有 %q：\n%s", s, out.String())
		}
	}
}

func TestSleepAllNothingRunning(t *testing.T) {
	f := &fakeComputers{screens: []screen.Screen{{BotID: "b", Name: "島灰", Box: "sleeping"}}}
	var out strings.Builder
	if err := sleepAll(context.Background(), f.call, &out); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 || !strings.Contains(out.String(), "沒有開著的電腦") {
		t.Errorf("calls = %v, out = %s", f.calls, out.String())
	}
}

func TestSleepAllReportsFailure(t *testing.T) {
	f := sampleComputers()
	f.fail = map[string]bool{"a": true}
	var out strings.Builder
	err := sleepAll(context.Background(), f.call, &out)
	if err == nil || !strings.Contains(err.Error(), "1 台") {
		t.Fatalf("err = %v", err)
	}
	// 一台失敗不影響其他台。
	if !slices.Contains(f.calls, "xbot.computers.sleep e") || !strings.Contains(out.String(), "水哥哥：主機連不上") {
		t.Errorf("calls = %v, out = %s", f.calls, out.String())
	}
}

func TestPurgeAllNeedsTypedConfirm(t *testing.T) {
	for _, answer := range []string{"\n", "y\n", "yes\n", "刪\n"} {
		f := sampleComputers()
		var out strings.Builder
		if err := purgeAll(context.Background(), f.call, false, bufio.NewReader(strings.NewReader(answer)), &out); err != nil {
			t.Fatal(err)
		}
		if len(f.calls) != 0 || !strings.Contains(out.String(), "已取消") {
			t.Errorf("回答 %q：calls = %v", answer, f.calls)
		}
	}
}

func TestPurgeAllEveryIdleBot(t *testing.T) {
	f := sampleComputers()
	var out strings.Builder
	if err := purgeAll(context.Background(), f.call, false, bufio.NewReader(strings.NewReader("刪除\n")), &out); err != nil {
		t.Fatal(err)
	}
	// 沒有電腦的（文案）也做，清掉留下的資料區；正在工作的（研究員）跳過。
	want := []string{"xbot.computers.purge a", "xbot.computers.purge b", "xbot.computers.purge d", "xbot.computers.purge e"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	for _, s := range []string{"即將刪除 5 個 Bot", "水哥哥、島灰、研究員、文案、驗證員", "Bot 本身、對話和設定都保留", "刪除 4 台"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("輸出沒有 %q：\n%s", s, out.String())
		}
	}
}

func TestPurgeAllYesSkipsPrompt(t *testing.T) {
	f := sampleComputers()
	var out strings.Builder
	if err := purgeAll(context.Background(), f.call, true, bufio.NewReader(strings.NewReader("")), &out); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 4 || strings.Contains(out.String(), "請輸入") {
		t.Errorf("calls = %v, out = %s", f.calls, out.String())
	}
}
