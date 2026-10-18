// @novnc/novnc 1.7 沒有附型別：只宣告用得到的部分（docs/API.md）。
declare module '@novnc/novnc' {
  export type RFBOptions = { shared?: boolean; credentials?: Record<string, string>; wsProtocols?: string[] }
  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: RFBOptions)
    viewOnly: boolean
    scaleViewport: boolean
    clipViewport: boolean
    resizeSession: boolean
    focusOnClick: boolean
    showDotCursor: boolean
    background: string
    qualityLevel: number
    compressionLevel: number
    disconnect(): void
    focus(options?: FocusOptions): void
    blur(): void
    sendKey(keysym: number, code: string | null, down?: boolean): void
    sendCtrlAltDel(): void
    clipboardPasteFrom(text: string): void
  }
}
