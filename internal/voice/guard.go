package voice

import (
	"encoding/json"
	"regexp"
)

// 用說的核定（E32，通話中的核定卡）：說「拒絕」一律可以；說「允許」只能處理低風險的事。
// 高風險的（confirm 級的操作、危險的引擎指令、花錢、對外發送、刪除、簽約、上架、不可逆）一定要在畫面上按。
// 前端從通話送出用說的決定時帶 via=voice；xbot.chat.respond、xbot.approvals.decide 在服務端照這裡再擋一次。

// ViaVoice 是「用說的」決定的標記（RespondIn.Via、DecideIn.Via）。
const ViaVoice = "voice"

// TapMessage 是用說的允許被擋下時的訊息（介面直接顯示）。
const TapMessage = "這件事要在畫面上按確認，不能只用說的"

// riskyText 是看得出高風險的字眼（中文、英文）。定價、調價不在裡面：L18 示範用說的核定調價。
var riskyText = regexp.MustCompile(`(?i)刪除|移除|清除|清空|銷毀|付款|支付|匯款|轉帳|花錢|花費|支出|購買|下單|扣款|刷卡|退款|金額|NT\$|\$\s?\d|\d\s*元|對外|寄出|寄信|發信|發送|發布|公開|上架|簽約|合約|不可逆|無法復原|危險|\bdelete\b|\bremove\b|\bdrop\b|\bdestroy\b|\bpay(ment)?\b|\bpurchase\b|\btransfer\b|\bsend\b|\bemail\b|\bpublish\b|\bcontract\b|irreversible|danger`)

// Risky 回答這段文字看不看得出高風險。
func Risky(texts ...string) bool {
	for _, t := range texts {
		if riskyText.MatchString(t) {
			return true
		}
	}
	return false
}

// EngineApprovalNeedsTap 回答 Bot 在對話裡要求核准的引擎動作（xbot.chat.respond 的 approval 卡）能不能用說的允許。
// 終端機指令一律要按：Hermes 只對它判斷危險的指令要核准；標了 danger／irreversible、或看得出高風險的也要按。
func EngineApprovalNeedsTap(payload json.RawMessage) bool {
	var p struct {
		Command      string `json:"command"`
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		Danger       bool   `json:"danger"`
		Irreversible bool   `json:"irreversible"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return true
	}
	return p.Command != "" || p.Danger || p.Irreversible || Risky(p.Description, p.Prompt)
}

// OpApprovalNeedsTap 回答等核准的操作（xbot.approvals.decide）能不能用說的允許。
// 會進核准的操作都是 confirm 級（刪除、提高上限、移除主機、對外發送、付款…），一律要按；
// 唯一的例外是 Bot 用 xbot.approvals.request 請你決定的事，看不出高風險時可以用說的。
func OpApprovalNeedsTap(action string, input json.RawMessage) bool {
	if action != "xbot.approvals.request" {
		return true
	}
	var in struct {
		Subject  string `json:"subject"`
		Details  string `json:"details"`
		Category string `json:"category"`
	}
	if json.Unmarshal(input, &in) != nil {
		return true
	}
	return Risky(in.Subject, in.Details, in.Category)
}
