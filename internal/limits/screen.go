package limits

import "time"

// 看 Bot 的畫面與接手（B14，PRD BX-7、BX-8）。

const (
	// VNCMessageMax 是 noVNC WebSocket 單一訊息的上限（兩個方向）。出自 LazyBoy server/main.py novnc_socket
	// （websockets connect max_size=16*1024*1024）。
	VNCMessageMax = 16 << 20

	// VNCDialTimeout 是服務連到 Bot 電腦裡 websockify 的時限。出自 LazyBoy server/main.py novnc()（httpx timeout=20）。
	VNCDialTimeout = 20 * time.Second

	// ViewerTouchInterval：有人在看畫面時，每隔這麼久記一次「電腦剛被使用」，讓閒置休眠（B13）不會在看的時候關機。
	// XBot 自訂：遠小於閒置休眠的 30 分鐘即可。
	ViewerTouchInterval = time.Minute

	// TakeoverIdle：接手中、但沒有人連著畫面超過這麼久，就自動交還（帶預設說明）。
	// LazyBoy 沒有這個數字（關掉視窗 Bot 就一直等）；XBot 取和閒置休眠相同的 30 分鐘（PRD BX-12），
	// 否則電腦會在接手中休眠，Bot 卻還在等。
	TakeoverIdle = 30 * time.Minute

	// TakeoverReasonMax 是 Bot 請你接手時說明的字數上限。
	TakeoverReasonMax = 500

	// HandbackNoteMax 是交還說明的字數上限。
	HandbackNoteMax = 2000

	// SnapshotTimeout 是擷取一張畫面縮圖的時限（docker exec import）。
	SnapshotTimeout = 10 * time.Second

	// SnapshotFresh：同一台電腦的縮圖這麼久內重複要求時，直接回上一張（辦公室頁面多個分頁同時輪詢也不會一直擷取）。
	SnapshotFresh = 4 * time.Second

	// SnapshotMaxBytes 是一張縮圖的大小上限。
	SnapshotMaxBytes = 1 << 20

	// SnapshotWidth、SnapshotHeight 是縮圖的最大寬高（等比例縮小，ImageMagick「WxH>」）；SnapshotQuality 是 JPEG 品質。
	// 辦公室卡片縮圖約 200×125，留兩倍給高解析度螢幕。
	SnapshotWidth   = 640
	SnapshotHeight  = 400
	SnapshotQuality = 60
)
