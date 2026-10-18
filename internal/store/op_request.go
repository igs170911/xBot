package store

import (
	"context"
	"encoding/json"
	"errors"
)

// ── op_request ───────────────────────────────────────

// OpRequest 是一次帶 request_id 的操作呼叫（MCP-4 去重）。同一個 Actor＋RequestID 只會做一次。
type OpRequest struct {
	Actor      string          `json:"actor"`
	RequestID  string          `json:"request_id"`
	Op         string          `json:"op"`
	ParamsHash string          `json:"params_hash"`
	Status     string          `json:"status"` // running / ok / pending_approval
	Result     json.RawMessage `json:"result"`
	ApprovalID string          `json:"approval_id"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
}

const opRequestCols = `actor, request_id, op, params_hash, status, result, approval_id, created_at, updated_at`

func scanOpRequest(s scanner) (OpRequest, error) {
	var r OpRequest
	var result string
	err := s.Scan(&r.Actor, &r.RequestID, &r.Op, &r.ParamsHash, &r.Status, &result, &r.ApprovalID, &r.CreatedAt, &r.UpdatedAt)
	r.Result = json.RawMessage(result)
	return r, err
}

// ClaimOpRequest 佔住 actor＋requestID（status＝running）。
// 第一次佔到時回傳 claimed＝true；已經有紀錄時回傳那筆紀錄、claimed＝false。
// 若舊紀錄停在 running 超過 staleMS 毫秒（程序中途當掉），視為沒有人在處理，改由這次接手。
func (q *Queries) ClaimOpRequest(ctx context.Context, actor, requestID, op, paramsHash string, staleMS int64) (OpRequest, bool, error) {
	var out OpRequest
	var claimed bool
	err := q.atomic(ctx, func(q *Queries) error {
		now := nowMS()
		res, err := q.w.ExecContext(ctx, `INSERT INTO op_request(`+opRequestCols+`) VALUES (?,?,?,?,'running','{}','',?,?)
			ON CONFLICT(actor, request_id) DO NOTHING`, actor, requestID, op, paramsHash, now, now)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 1 {
			claimed = true
			out = OpRequest{Actor: actor, RequestID: requestID, Op: op, ParamsHash: paramsHash, Status: "running",
				Result: json.RawMessage("{}"), CreatedAt: now, UpdatedAt: now}
			return nil
		}
		out, err = scanOpRequest(q.w.QueryRowContext(ctx, `SELECT `+opRequestCols+` FROM op_request WHERE actor=? AND request_id=?`, actor, requestID))
		if err != nil {
			return err
		}
		if out.Status == "running" && out.Op == op && out.ParamsHash == paramsHash && now-out.UpdatedAt > staleMS {
			if _, err := q.w.ExecContext(ctx, `UPDATE op_request SET updated_at=? WHERE actor=? AND request_id=?`, now, actor, requestID); err != nil {
				return err
			}
			out.UpdatedAt, claimed = now, true
		}
		return nil
	})
	return out, claimed, err
}

// FinishOpRequest 記下結果：status 是 ok（result 是輸出）或 pending_approval（approvalID 是等待的核准）。
func (q *Queries) FinishOpRequest(ctx context.Context, actor, requestID, status string, result json.RawMessage, approvalID string) error {
	if status != "ok" && status != "pending_approval" {
		return errors.New("op_request 的狀態只能是 ok 或 pending_approval")
	}
	return affected(q.w.ExecContext(ctx, `UPDATE op_request SET status=?, result=?, approval_id=?, updated_at=? WHERE actor=? AND request_id=?`,
		status, jsonOr(result, "{}"), approvalID, nowMS(), actor, requestID))
}

// ReleaseOpRequest 刪掉紀錄（呼叫失敗時），讓同一個 request_id 可以重試。
func (q *Queries) ReleaseOpRequest(ctx context.Context, actor, requestID string) error {
	_, err := q.w.ExecContext(ctx, `DELETE FROM op_request WHERE actor=? AND request_id=?`, actor, requestID)
	return err
}

// GetOpRequest 讀一筆去重紀錄。
func (q *Queries) GetOpRequest(ctx context.Context, actor, requestID string) (OpRequest, error) {
	r, err := scanOpRequest(q.r.QueryRowContext(ctx, `SELECT `+opRequestCols+` FROM op_request WHERE actor=? AND request_id=?`, actor, requestID))
	return r, notFound(err)
}

// GetOpRequestByApproval 找出在等某個核准的那筆紀錄。
func (q *Queries) GetOpRequestByApproval(ctx context.Context, approvalID string) (OpRequest, error) {
	r, err := scanOpRequest(q.r.QueryRowContext(ctx, `SELECT `+opRequestCols+` FROM op_request WHERE approval_id=? AND approval_id <> ''`, approvalID))
	return r, notFound(err)
}

// StartApprovedOpRequest 在核准後開始執行：把等這個核准的紀錄從 pending_approval 改成 running。
// 改到了回傳 started＝true；已經在執行或執行完（例：按了兩次允許）回傳現在的紀錄、started＝false。
func (q *Queries) StartApprovedOpRequest(ctx context.Context, approvalID string) (OpRequest, bool, error) {
	var out OpRequest
	var started bool
	err := q.atomic(ctx, func(q *Queries) error {
		res, err := q.w.ExecContext(ctx, `UPDATE op_request SET status='running', updated_at=? WHERE approval_id=? AND approval_id <> '' AND status='pending_approval'`,
			nowMS(), approvalID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		started = n == 1
		out, err = scanOpRequest(q.w.QueryRowContext(ctx, `SELECT `+opRequestCols+` FROM op_request WHERE approval_id=? AND approval_id <> ''`, approvalID))
		return notFound(err)
	})
	return out, started, err
}

// ReadOnly 回傳同一個資料庫的唯讀版本：所有寫入（含 Tx）都會失敗。
// 給 dry_run 的操作用，保證預演時不會寫到資料。關閉它不會關掉原本的資料庫。
func (db *DB) ReadOnly() *DB {
	return &DB{Queries: &Queries{w: db.read, r: db.read}, write: db.read, read: db.read, path: db.path, readOnly: true}
}
