package ops

// 代為核准的類別（MCP-10，design/J12「哪些類別」）。操作用 Op.Category 標出自己屬於哪一類；
// 使用者授權代理時逐類勾選。刪除 Bot 或資料、移除主機、提高花費上限、修改代為核准授權的操作設 NoDelegate，
// 不管授權怎麼設都不能代為核准。
const (
	CategoryExternalSend = "external_send" // 對外發送：寄信、發訊息、發貼文
	CategorySpendSmall   = "spend_small"   // 小額花費：付款、訂購（帶金額，算每日金額上限）
	CategoryPublish      = "publish"       // 上架：商品、頁面、應用程式上架或公開
	CategorySign         = "sign"          // 簽約：接受條款、簽署文件
)

// Categories 是所有代為核准的類別，依畫面上的順序。
var Categories = []string{CategoryExternalSend, CategorySpendSmall, CategoryPublish, CategorySign}

// CategoryNames 是類別給人看的名字。
var CategoryNames = map[string]string{
	CategoryExternalSend: "對外發送",
	CategorySpendSmall:   "小額花費",
	CategoryPublish:      "上架",
	CategorySign:         "簽約",
}

// Category 是代為核准類別的型別（給 JSON Schema 列出可用值）。
type Category string

// EnumValues 讓 JSON Schema 列出可用值。
func (Category) EnumValues() []string { return Categories }

// Delegable 回答這個操作能不能代為核准：要有類別，而且不是 NoDelegate。
func (i Info) Delegable() bool { return i.Category != "" && !i.NoDelegate }
