package skills

import (
	"context"
	"errors"

	"xbot/internal/boxfs"
)

// 分享與匯入 Bot（G13、G14，BT-8）用的技能：只帶文字（名稱、什麼時候用、怎麼做），不帶附帶的檔案。

// Shared 是 .xbot 檔裡的一個技能。
type Shared struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

// ExportBot 列出 Bot 自己的技能（不含 Hermes、XBot 內建、沒改過的），給分享用。
// Bot 還沒有電腦時回 (nil, false, nil)：技能在電腦裡，第一次開機前沒有自己的技能。
func (s *Service) ExportBot(ctx context.Context, botID string) ([]Shared, bool, error) {
	if s == nil || s.Locate == nil {
		return nil, false, nil
	}
	f, err := s.Locate(ctx, botID, false)
	if errors.Is(err, boxfs.ErrNoComputer) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	all, err := Bot{FS: f, BotID: botID}.List(ctx)
	if err != nil {
		return nil, true, err
	}
	out := []Shared{}
	for _, x := range all {
		if x.Source == "hermes" || x.Source == "xbot" {
			continue
		}
		out = append(out, Shared{Name: x.Name, Description: x.Description, Instructions: x.Instructions})
	}
	return out, true, nil
}

// LibraryNames 回傳工作區技能庫已經有的技能名稱（匯入前檢查重複用）。
func (s *Service) LibraryNames() (map[string]bool, error) {
	out := map[string]bool{}
	if s == nil || s.Library == nil {
		return out, nil
	}
	all, err := s.Library.List()
	if err != nil {
		return nil, err
	}
	for _, x := range all {
		out[x.Name] = true
	}
	return out, nil
}

// ImportToLibrary 把分享檔裡的技能放進工作區技能庫，一律先停用（SK-1：從外面來的技能要你看過內容再打開）。
// 技能庫已經有同名的就合併（不重複加入），回傳在 merged。origin 記來源（例：「小秘.xbot」）。
func (s *Service) ImportToLibrary(list []Shared, origin, actor string) (added []LibSkill, merged []string, err error) {
	if s == nil || s.Library == nil {
		return nil, nil, errors.New("技能服務沒有啟動")
	}
	for _, x := range list {
		d := Doc{Name: oneLine(x.Name), Description: oneLine(x.Description), Instructions: x.Instructions}
		got, err := s.Library.Create(d, nil, libMetaData{Enabled: false, Source: "imported", Origin: origin, CreatedBy: actor})
		switch {
		case errors.Is(err, ErrExists):
			merged = append(merged, d.Name)
		case err != nil:
			return added, merged, err
		default:
			added = append(added, got)
		}
	}
	return added, merged, nil
}
