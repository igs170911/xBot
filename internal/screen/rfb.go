package screen

import (
	"encoding/binary"
	"errors"
	"fmt"

	"xbot/internal/limits"
)

// viewOnly 是「只能看」的 RFB 過濾器：讀瀏覽器送往 Bot 電腦的位元組串流（RFB 3.3／3.7／3.8，RFC 6143），
// 握手和「要畫面」的訊息照送，鍵盤、滑鼠、剪貼簿、改解析度、xvp（關機重開）一律丟掉。
// 認不得的訊息直接斷線（寧可斷，也不放過輸入）。
//
// 只能看是在服務這一端強制的：拿到 view 連線的人改了自己的 noVNC 也打不出字。接手（control）的連線不經過它。
type viewOnly struct {
	state int
	minor int // 用戶端的協定版本（3.x 的 x）
	buf   []byte
}

const (
	rfbVersion = iota
	rfbSecurity
	rfbInit
	rfbMessages
)

// RFB 用戶端訊息（RFC 6143 §7.5；extension：EnableContinuousUpdates、Fence、SetDesktopSize、xvp、QEMU）。
const (
	msgSetPixelFormat   = 0
	msgSetEncodings     = 2
	msgFBUpdateRequest  = 3
	msgKeyEvent         = 4
	msgPointerEvent     = 5
	msgClientCutText    = 6
	msgContinuousUpdate = 150
	msgFence            = 248
	msgXvp              = 250
	msgSetDesktopSize   = 251
	msgQEMU             = 255
	securityNone        = 1
)

var errRFB = errors.New("畫面連線的資料不正確")

// Write 收一段瀏覽器送來的資料，回傳可以轉給 Bot 電腦的部分。不完整的訊息留到下一段。
func (v *viewOnly) Write(in []byte) ([]byte, error) {
	v.buf = append(v.buf, in...)
	var out []byte
	for {
		n, keep, err := v.next()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			break
		}
		if keep {
			out = append(out, v.buf[:n]...)
		}
		v.buf = v.buf[n:]
	}
	if len(v.buf) > limits.VNCMessageMax {
		return nil, fmt.Errorf("%w：訊息太大", errRFB)
	}
	// 留下的半截訊息搬到新的陣列，不要一直拖著前面已經處理過的部分。
	v.buf = append([]byte(nil), v.buf...)
	return out, nil
}

// next 看 buf 開頭的一則訊息：n 是它的長度（0＝還不完整），keep 是要不要轉送。
func (v *viewOnly) next() (n int, keep bool, err error) {
	b := v.buf
	switch v.state {
	case rfbVersion:
		if len(b) < 12 {
			return 0, false, nil
		}
		var major int
		if _, err := fmt.Sscanf(string(b[:12]), "RFB %03d.%03d\n", &major, &v.minor); err != nil || major != 3 {
			return 0, false, fmt.Errorf("%w：協定版本", errRFB)
		}
		if v.minor < 7 {
			v.state = rfbInit // 3.3：安全類型由伺服器決定，用戶端不用選
		} else {
			v.state = rfbSecurity
		}
		return 12, true, nil
	case rfbSecurity:
		if len(b) < 1 {
			return 0, false, nil
		}
		if b[0] != securityNone {
			return 0, false, fmt.Errorf("%w：不支援的安全類型 %d", errRFB, b[0])
		}
		v.state = rfbInit
		return 1, true, nil
	case rfbInit:
		if len(b) < 1 {
			return 0, false, nil
		}
		b[0] = 1 // shared：只能看的人不能把其他連線（Bot 用的、接手的人）踢掉
		v.state = rfbMessages
		return 1, true, nil
	}
	if len(b) < 1 {
		return 0, false, nil
	}
	need := func(size int) (int, bool, error) {
		if len(b) < size {
			return 0, false, nil
		}
		return size, true, nil
	}
	drop := func(size int) (int, bool, error) {
		if len(b) < size {
			return 0, false, nil
		}
		return size, false, nil
	}
	switch b[0] {
	case msgSetPixelFormat:
		return need(20)
	case msgSetEncodings:
		if len(b) < 4 {
			return 0, false, nil
		}
		return need(4 + 4*int(binary.BigEndian.Uint16(b[2:4])))
	case msgFBUpdateRequest:
		return need(10)
	case msgContinuousUpdate:
		return need(10)
	case msgFence:
		if len(b) < 9 {
			return 0, false, nil
		}
		return need(9 + int(b[8]))
	case msgKeyEvent:
		return drop(8)
	case msgPointerEvent:
		if len(b) < 2 {
			return 0, false, nil
		}
		if b[1]&0x80 != 0 { // ExtendedMouseButtons：多一個位元組
			return drop(7)
		}
		return drop(6)
	case msgClientCutText:
		if len(b) < 8 {
			return 0, false, nil
		}
		size := int64(int32(binary.BigEndian.Uint32(b[4:8])))
		if size < 0 {
			size = -size // extended clipboard：長度是負數
		}
		if size > limits.VNCMessageMax {
			return 0, false, fmt.Errorf("%w：剪貼簿太大", errRFB)
		}
		return drop(8 + int(size))
	case msgXvp:
		return drop(4)
	case msgSetDesktopSize:
		if len(b) < 8 {
			return 0, false, nil
		}
		return drop(8 + 16*int(b[6]))
	case msgQEMU:
		if len(b) < 2 {
			return 0, false, nil
		}
		if b[1] != 0 {
			return 0, false, fmt.Errorf("%w：不認得的 QEMU 訊息 %d", errRFB, b[1])
		}
		return drop(12)
	}
	return 0, false, fmt.Errorf("%w：不認得的訊息 %d", errRFB, b[0])
}
