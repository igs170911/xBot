package screen

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func be16(v int) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, uint16(v)); return b }
func be32(v int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// 只能看：握手、要畫面的訊息照送；鍵盤、滑鼠、剪貼簿、改解析度、xvp 丟掉；資料被切成任意小段也一樣。
func TestViewOnlyFilter(t *testing.T) {
	handshake := cat([]byte("RFB 003.008\n"), []byte{securityNone}, []byte{0})
	pixel := cat([]byte{msgSetPixelFormat}, make([]byte, 19))
	enc := cat([]byte{msgSetEncodings, 0}, be16(2), be32(0), be32(-239))
	req := cat([]byte{msgFBUpdateRequest, 0}, make([]byte, 8))
	cont := cat([]byte{msgContinuousUpdate, 1}, make([]byte, 8))
	fence := cat([]byte{msgFence, 0, 0, 0}, be32(0), []byte{3, 'a', 'b', 'c'})
	key := cat([]byte{msgKeyEvent, 1, 0, 0}, be32(0x61))
	ptr := []byte{msgPointerEvent, 1, 0, 10, 0, 20}
	extPtr := []byte{msgPointerEvent, 0x81, 0, 10, 0, 20, 0}
	cut := cat([]byte{msgClientCutText, 0, 0, 0}, be32(5), []byte("hello"))
	extCut := cat([]byte{msgClientCutText, 0, 0, 0}, be32(-4), []byte{1, 0, 0, 1})
	resize := cat([]byte{msgSetDesktopSize, 0}, be16(800), be16(600), []byte{1, 0}, make([]byte, 16))
	xvp := []byte{msgXvp, 0, 1, 4}
	qemu := cat([]byte{msgQEMU, 0}, be16(1), be32(0x61), be32(30))

	in := cat(handshake, pixel, enc, key, req, ptr, extPtr, cut, extCut, cont, resize, xvp, qemu, fence)
	want := cat([]byte("RFB 003.008\n"), []byte{securityNone, 1}, pixel, enc, req, cont, fence)

	for _, step := range []int{len(in), 1, 3, 7} {
		v := &viewOnly{}
		var got []byte
		for i := 0; i < len(in); i += step {
			end := min(i+step, len(in))
			out, err := v.Write(in[i:end])
			if err != nil {
				t.Fatalf("step %d：%v", step, err)
			}
			got = append(got, out...)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("step %d：\n got %v\nwant %v", step, got, want)
		}
	}
}

func TestViewOnlyRejects(t *testing.T) {
	for name, in := range map[string][]byte{
		"版本":     []byte("HTTP/1.1 200\n"),
		"VNC 密碼": cat([]byte("RFB 003.008\n"), []byte{2}),
		"不認得的訊息": cat([]byte("RFB 003.008\n"), []byte{securityNone, 1}, []byte{99, 0, 0, 0}),
		"QEMU":   cat([]byte("RFB 003.008\n"), []byte{securityNone, 1}, []byte{msgQEMU, 1, 0, 0}),
	} {
		v := &viewOnly{}
		if _, err := v.Write(in); !errors.Is(err, errRFB) {
			t.Errorf("%s：%v", name, err)
		}
	}
	// 3.3：用戶端不選安全類型，直接 ClientInit。
	v := &viewOnly{}
	out, err := v.Write(cat([]byte("RFB 003.003\n"), []byte{0}, []byte{msgKeyEvent, 1, 0, 0}, be32(1)))
	if err != nil || !bytes.Equal(out, cat([]byte("RFB 003.003\n"), []byte{1})) {
		t.Fatalf("3.3：%v %v", out, err)
	}
}
