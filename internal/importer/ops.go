package importer

import (
	"context"
	"errors"

	"xbot/internal/ops"
)

// DetectIn 是 xbot.import.detect 的輸入。
type DetectIn struct {
	Path string `json:"path,omitempty" max:"1000" desc:"只看這個資料夾（LazyBoy 的 .data、OpenMausBot 的 ~/.openmausbot）；不填找預設的位置"`
}

// DetectOut 是 xbot.import.detect 的輸出。
type DetectOut struct {
	Sources []Found `json:"sources"`
}

// PlanIn 是 xbot.import.plan 的輸入。
type PlanIn struct {
	Source string   `json:"source,omitempty" max:"80" desc:"xbot.import.detect 的 id"`
	Path   string   `json:"path,omitempty" max:"1000" desc:"或直接給舊資料的資料夾"`
	Names  []Rename `json:"names,omitempty" max:"200" desc:"改 Bot 的名字（預演裡看重名）"`
}

// StatusIn 是 xbot.import.status 的輸入。
type StatusIn struct{}

// StatusOut 是 xbot.import.status 的輸出。
type StatusOut struct {
	Running bool  `json:"running"`
	Runs    []Run `json:"runs" desc:"最近的匯入（新的在前）"`
}

func ownerOnly(call *ops.Call) error {
	if call.Identity.Kind != ops.KindOwner {
		return ops.Forbidden("只有你本人能從舊系統匯入（會讀這台電腦上的檔案與金鑰）")
	}
	return nil
}

// Register 登錄 xbot.import.detect、plan、run、status。s 是 nil 時只登錄定義。
func Register(reg *ops.Registry, s *Service) {
	need := func() error {
		if s == nil {
			return ops.Internal(errors.New("匯入服務沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[DetectIn, DetectOut]{
		Name: "xbot.import.detect", Summary: "找這台電腦上 LazyBoy、OpenMausBot 的舊資料",
		Description: "列出找到的舊資料與可以帶進來的東西：Bot、技能、記憶、定時任務、團隊、群組、模型金鑰、瀏覽器登入。只讀，不改舊資料。" +
			"預設找 LazyBoy 的 .data 與 OpenMausBot 的 ~/.openmausbot；path 只看指定的資料夾。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in DetectIn) (DetectOut, error) {
			if err := need(); err != nil {
				return DetectOut{}, err
			}
			if err := ownerOnly(call); err != nil {
				return DetectOut{}, err
			}
			list, err := s.Detect(ctx, in.Path)
			if err != nil {
				return DetectOut{}, sourceErr(err)
			}
			return DetectOut{Sources: list}, nil
		},
	})
	ops.Register(reg, ops.Op[PlanIn, PlanOut]{
		Name: "xbot.import.plan", Summary: "預演匯入：逐項列出會帶什麼",
		Description: "列出每個 Bot 與它的技能、記憶、電腦裡的檔案、瀏覽器登入、定時任務，以及團隊、群組、模型金鑰；每項有預設勾不勾。" +
			"已經帶過的標 done（再匯入不會重複建立），和現有 Bot 同名的自動加編號。不建立任何東西。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in PlanIn) (PlanOut, error) {
			if err := need(); err != nil {
				return PlanOut{}, err
			}
			if err := ownerOnly(call); err != nil {
				return PlanOut{}, err
			}
			src, found, err := s.resolve(ctx, in.Source, in.Path)
			if err != nil {
				return PlanOut{}, sourceErr(err)
			}
			renames := map[string]string{}
			for _, r := range in.Names {
				renames[r.Key] = r.Name
			}
			p, err := s.buildPlan(ctx, call.DB, src, found, renames)
			if err != nil {
				return PlanOut{}, err
			}
			return p.out, nil
		},
	})
	ops.Register(reg, ops.Op[RunIn, Run]{
		Name: "xbot.import.run", Summary: "從 LazyBoy 或 OpenMausBot 匯入勾選的項目",
		Description: "照 xbot.import.plan 的項目代號建立：Bot 一律從「先問我」開始（SEC-1），技能放進它自己的技能並先關閉，" +
			"定時任務先暫停，模型金鑰加密後只放在服務端。對話紀錄不帶。在背景進行，進度看 xbot.import.status 或 import.progress 事件；" +
			"wait=true 等做完才回。已經帶過的不會重複建立。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in RunIn) (Run, error) {
			if err := need(); err != nil {
				return Run{}, err
			}
			if err := ownerOnly(call); err != nil {
				return Run{}, err
			}
			if call.DryRun {
				return Run{State: "preview", Results: []Result{}, Bots: []string{}}, nil
			}
			return s.Start(ctx, call.DB, call.Identity.Actor(), in)
		},
	})
	ops.Register(reg, ops.Op[StatusIn, StatusOut]{
		Name: "xbot.import.status", Summary: "匯入的進度與結果",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in StatusIn) (StatusOut, error) {
			if err := need(); err != nil {
				return StatusOut{}, err
			}
			runs := s.Runs()
			return StatusOut{Runs: runs, Running: len(runs) > 0 && runs[0].State == RunRunning}, nil
		},
	})
}
