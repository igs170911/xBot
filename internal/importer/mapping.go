package importer

import (
	"context"
	"errors"

	"xbot/internal/store"
)

// 帶進來的東西的種類（import_map.kind，也是預演項目的 kind）。
const (
	ItemBot       = "bot"
	ItemSkills    = "skills"
	ItemMemory    = "memory"
	ItemData      = "data"
	ItemLogin     = "login"
	ItemRoutine   = "routine"
	ItemTeam      = "team"
	ItemRoom      = "room"
	ItemKey       = "key"
	ItemApprovals = "approvals"
)

// mapping 是已經帶進來的東西（import_map），用來判斷再匯入時哪些不用再建。
type mapping struct {
	source string
	m      map[string]store.ImportRecord
}

func mapKey(kind, sid string) string { return kind + "\x00" + sid }

func loadMap(ctx context.Context, db *store.DB, source string) (*mapping, error) {
	rows, err := db.ListImports(ctx, source)
	if err != nil {
		return nil, err
	}
	m := &mapping{source: source, m: map[string]store.ImportRecord{}}
	for _, r := range rows {
		m.m[mapKey(r.Kind, r.SourceID)] = r
	}
	return m, nil
}

func (m *mapping) put(r store.ImportRecord) { m.m[mapKey(r.Kind, r.SourceID)] = r }

// bot 回傳舊 Bot 帶進來後的 Bot id（那個 Bot 還在才算）。
func (m *mapping) bot(ctx context.Context, db *store.DB, sid string) (string, bool) {
	r, ok := m.m[mapKey(ItemBot, sid)]
	if !ok {
		return "", false
	}
	if _, err := db.GetBot(ctx, r.TargetID); err != nil {
		return "", false
	}
	return r.TargetID, true
}

// done 回答一樣東西是不是已經帶進來了（目標還在）。
func (m *mapping) done(ctx context.Context, db *store.DB, kind, sid string) (string, bool) {
	r, ok := m.m[mapKey(kind, sid)]
	if !ok {
		return "", false
	}
	var err error
	switch kind {
	case ItemBot, ItemSkills, ItemMemory, ItemData, ItemLogin:
		_, err = db.GetBot(ctx, r.TargetID)
	case ItemRoutine:
		_, err = db.GetRoutine(ctx, r.TargetID)
	case ItemTeam:
		_, err = db.GetTeam(ctx, r.TargetID)
	case ItemRoom:
		_, err = db.GetRoom(ctx, r.TargetID)
	}
	if errors.Is(err, store.ErrNotFound) {
		return "", false
	}
	return r.TargetID, err == nil
}
