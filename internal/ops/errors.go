package ops

import (
	"errors"
	"fmt"
	"strings"

	"xbot/internal/store"
)

// Code 是固定的錯誤代碼（MCP-5）。呼叫端依代碼判斷，不要解析 Message。
type Code string

const (
	CodeForbidden       Code = "forbidden"        // 身分或權限範圍不夠
	CodeNotFound        Code = "not_found"        // 操作或資料不存在
	CodeInvalidInput    Code = "invalid_input"    // 輸入不對；Fields 列出哪些欄位
	CodeLimit           Code = "limit"            // 超過固定限制（PRD §18）
	CodeBudget          Code = "budget"           // 超過花費上限（PRD §6.13）
	CodePendingApproval Code = "pending_approval" // 已送到「待我處理」，NeedsApprovalID 是核准 id
	CodeConflict        Code = "conflict"         // 和現況衝突（例：同一個 request_id 用在不同內容）
	CodeInternal        Code = "internal"         // 系統內部錯誤
)

// FieldError 是一個輸入欄位的問題。Field 是路徑，例：name、members[0].bot_id。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error 是所有操作回傳的錯誤格式（MCP-5）：{code, message, retryable, needs_approval_id?}。
// Message 是給人看的白話中文。
type Error struct {
	Code            Code         `json:"code"`
	Message         string       `json:"message"`
	Retryable       bool         `json:"retryable"`
	NeedsApprovalID string       `json:"needs_approval_id,omitempty"`
	Fields          []FieldError `json:"fields,omitempty"`

	cause error // 內部原因，只寫進紀錄，不回給呼叫端
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s：%s（%v）", e.Code, e.Message, e.cause)
	}
	return string(e.Code) + "：" + e.Message
}

// Unwrap 讓 errors.Is 看得到內部原因。
func (e *Error) Unwrap() error { return e.cause }

// Errorf 產生一個指定代碼的錯誤。
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Retryable: code == CodeInternal}
}

// Forbidden、NotFound、Conflict、Limit、Budget 是常用錯誤的捷徑。
func Forbidden(msg string) *Error { return &Error{Code: CodeForbidden, Message: msg} }
func NotFound(msg string) *Error  { return &Error{Code: CodeNotFound, Message: msg} }
func Conflict(msg string) *Error  { return &Error{Code: CodeConflict, Message: msg} }
func Limit(msg string) *Error     { return &Error{Code: CodeLimit, Message: msg} }
func Budget(msg string) *Error    { return &Error{Code: CodeBudget, Message: msg} }

// InvalidInput 產生一個輸入錯誤，訊息會列出有問題的欄位。
func InvalidInput(fields ...FieldError) *Error {
	parts := make([]string, 0, len(fields))
	for i, f := range fields {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("還有 %d 個", len(fields)-3))
			break
		}
		if f.Field == "" {
			parts = append(parts, f.Message)
		} else {
			parts = append(parts, f.Field+" "+f.Message)
		}
	}
	return &Error{Code: CodeInvalidInput, Message: "輸入有問題：" + strings.Join(parts, "；"), Fields: fields}
}

// Internal 包住一個內部錯誤；回給呼叫端的只有白話訊息，原因留在 cause。
func Internal(cause error) *Error {
	return &Error{Code: CodeInternal, Message: "系統出了點問題，請稍後再試", Retryable: true, cause: cause}
}

// AsError 把任何錯誤轉成 *Error：已經是 *Error 就原樣回傳，store.ErrNotFound 變成 not_found，其他都是 internal。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, store.ErrNotFound) {
		return &Error{Code: CodeNotFound, Message: "找不到資料", cause: err}
	}
	if errors.Is(err, store.ErrAlreadyDecided) {
		return &Error{Code: CodeConflict, Message: store.ErrAlreadyDecided.Error(), cause: err}
	}
	return Internal(err)
}
