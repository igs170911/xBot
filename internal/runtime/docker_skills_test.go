//go:build docker

// C31：在真的 Bot 電腦裡寫技能與記憶，再讀回來，並確認 Hermes 自己讀得到（電腦沒開機時也能寫）。
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/engine/hermes"
	"xbot/internal/memory"
	"xbot/internal/skills"
)

// hermesSees 在電腦裡用 Hermes 自己的程式讀記憶與（沒停用的）技能名稱。
const hermesSees = `
import json
from tools.memory_tool_store import MemoryStore
from tools.skills_tool import _find_all_skills
s = MemoryStore(); s.load_from_disk()
print(json.dumps({"memory": s.memory_entries, "user": s.user_entries,
                  "skills": sorted(x["name"] for x in _find_all_skills())}, ensure_ascii=False))
`

func execOut(ctx context.Context, t *testing.T, cli *client.Client, id string, cmd ...string) string {
	t.Helper()
	created, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{User: strconv.Itoa(box.BotUID), AttachStdout: true, AttachStderr: true,
		Env: []string{"HERMES_HOME=" + hermes.HomeDir}, WorkingDir: "/opt/hermes", Cmd: cmd})
	if err != nil {
		t.Fatal(err)
	}
	att, err := cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Close()
	var out, errb bytes.Buffer
	_, _ = stdcopy.StdCopy(&out, &errb, att.Reader)
	if errb.Len() > 0 {
		t.Logf("stderr：%s", errb.String())
	}
	return out.String()
}

func TestDockerSkillsAndMemory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env := newDockerEnv(ctx, t)
	comp, botID := env.comp, env.bot.ID

	// 還沒有電腦：不建立。
	if _, err := comp.BotFS(ctx, botID, false); err == nil {
		t.Fatal("還沒有電腦時應回 ErrNoComputer")
	}
	// 電腦建好但沒開機時寫入。
	f, err := comp.BotFS(ctx, botID, true)
	if err != nil {
		t.Fatal(err)
	}
	b := skills.Bot{FS: f, BotID: botID}
	name, desc, steps := "weekly-report", "每週一彙整營收", "1. 讀營收\n2. 寫成表格"
	made, err := b.Create(ctx, skills.Edit{Name: &name, Description: &desc, Instructions: &steps})
	if err != nil {
		t.Fatal(err)
	}
	off, offDesc := "old-way", "舊做法"
	if _, err := b.Create(ctx, skills.Edit{Name: &off, Description: &offDesc, Instructions: &steps, Place: skills.PlaceOff}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := memory.Write(ctx, f, memory.TargetMemory, []string{"客戶 A 用月結", "週一開會"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := memory.Write(ctx, f, memory.TargetUser, []string{"喜歡簡短回答"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get(ctx, made.ID)
	if err != nil || got.Instructions != steps || got.Place != skills.PlaceList {
		t.Fatalf("讀回技能：%+v %v", got, err)
	}
	st, err := memory.Read(ctx, f)
	if err != nil || !slices.Equal(st.Memory.Entries, []string{"客戶 A 用月結", "週一開會"}) || st.User.Entries[0] != "喜歡簡短回答" {
		t.Fatalf("讀回記憶：%+v %v", st, err)
	}

	// 開機：Prepare 放入內建技能、寫 Hermes 設定（保留技能的開關）。
	if _, err := env.comp.Boxes.Start(ctx, botID); err != nil {
		t.Fatalf("開機：%v\n%s", err, env.logs())
	}
	f, err = comp.BotFS(ctx, botID, false)
	if err != nil {
		t.Fatal(err)
	}
	b = skills.Bot{FS: f, BotID: botID}
	all, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, s := range all {
		sources[s.Name] = s.Source
	}
	if sources["deep-research"] != "xbot" || sources["weekly-report"] != "custom" {
		t.Fatalf("內建技能與自訂技能：%v", sources)
	}
	s, _ := b.Get(ctx, "old-way")
	if s.Place != skills.PlaceOff {
		t.Fatal("Prepare 改寫 config.yaml 時要保留停用的技能")
	}

	// Hermes 自己讀得到：記憶的兩份清單、沒停用的技能（停用的不在）。
	bx, _ := env.db.GetBox(ctx, botID)
	var seen struct {
		Memory []string `json:"memory"`
		User   []string `json:"user"`
		Skills []string `json:"skills"`
	}
	out := execOut(ctx, t, env.cli, bx.ContainerID, hermes.Python, "-c", hermesSees)
	if err := json.Unmarshal([]byte(strings.TrimSpace(out[strings.LastIndex(strings.TrimSpace(out), "\n")+1:])), &seen); err != nil {
		t.Fatalf("讀不懂 Hermes 的輸出：%q %v", out, err)
	}
	if !slices.Equal(seen.Memory, []string{"客戶 A 用月結", "週一開會"}) || !slices.Equal(seen.User, []string{"喜歡簡短回答"}) {
		t.Fatalf("Hermes 讀到的記憶：%+v", seen)
	}
	if !slices.Contains(seen.Skills, "weekly-report") || slices.Contains(seen.Skills, "old-way") || !slices.Contains(seen.Skills, "deep-research") {
		t.Fatalf("Hermes 讀到的技能：%v", seen.Skills)
	}
	// 檔案屬於 Bot 使用者（Hermes 自己也要能改）。
	if uid := strings.TrimSpace(execOut(ctx, t, env.cli, bx.ContainerID, "stat", "-c", "%u", skills.SkillsDir+"/"+made.ID+"/SKILL.md", memory.Dir+"/MEMORY.md")); uid != strconv.Itoa(box.BotUID)+"\n"+strconv.Itoa(box.BotUID) {
		t.Fatalf("擁有者：%q", uid)
	}

	// 刪除（要開機才能刪）。
	if _, err := b.Delete(ctx, made.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, made.ID); err == nil {
		t.Fatal("刪掉了")
	}

	// 改了人設：SOUL.md 立刻更新。
	bot := env.bot
	bot.Soul = "說話簡短、先講結論"
	if _, err := env.db.UpdateBot(ctx, bot); err != nil {
		t.Fatal(err)
	}
	if err := comp.RefreshSoul(ctx, botID); err != nil {
		t.Fatal(err)
	}
	soul, err := f.ReadFile(ctx, hermes.HomeDir+"/"+hermes.SoulFile)
	if err != nil || !strings.Contains(string(soul), "說話簡短、先講結論") {
		t.Fatalf("SOUL.md：%q %v", soul, err)
	}
}
