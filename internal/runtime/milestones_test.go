package runtime

import (
	"testing"
	"xbot/internal/engine"
)

func TestIsMilestone(t *testing.T) {
	tests := []struct {
		name     string
		event    engine.Event
		expected bool
	}{
		{
			name: "背景工作是里程碑",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: &engine.ToolEvent{Name: "背景工作：deep-research", ID: "bg1"},
			},
			expected: true,
		},
		{
			name: "瀏覽器操作是里程碑",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: &engine.ToolEvent{Name: "browser_open", ID: "t1"},
			},
			expected: true,
		},
		{
			name: "網路請求是里程碑",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: &engine.ToolEvent{Name: "web_search", ID: "t2"},
			},
			expected: true,
		},
		{
			name: "訊息傳送是里程碑",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: &engine.ToolEvent{Name: "message_bot", ID: "t3"},
			},
			expected: true,
		},
		{
			name: "MCP 工具是里程碑",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: &engine.ToolEvent{Name: "mcp_github_search", ID: "t4"},
			},
			expected: true,
		},
		{
			name: "檔案讀取不是里程碑",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: &engine.ToolEvent{Name: "read_file", ID: "t5"},
			},
			expected: false,
		},
		{
			name: "終端機指令不是里程碑",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: &engine.ToolEvent{Name: "terminal", ID: "t6"},
			},
			expected: false,
		},
		{
			name: "計畫階段是里程碑",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: &engine.ToolEvent{
					Name:    "terminal",
					ID:      "t7",
					Context: "開始建置專案",
				},
			},
			expected: true,
		},
		{
			name: "沒有 Tool 不是里程碑",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: nil,
			},
			expected: false,
		},
		{
			name: "非 ToolCall 事件不是里程碑",
			event: engine.Event{
				Type: engine.EventText,
				Tool: &engine.ToolEvent{Name: "browser_open", ID: "t8"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMilestone(tt.event)
			if got != tt.expected {
				t.Errorf("isMilestone() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestShouldNotifyToolResult(t *testing.T) {
	tests := []struct {
		name          string
		event         engine.Event
		lastMilestone string
		expected      bool
	}{
		{
			name: "里程碑工具的結果需要通知",
			event: engine.Event{
				Type: engine.EventToolResult,
				Tool: &engine.ToolEvent{ID: "bg1", Name: "背景工作：research"},
			},
			lastMilestone: "bg1",
			expected:      true,
		},
		{
			name: "非里程碑工具的結果不通知",
			event: engine.Event{
				Type: engine.EventToolResult,
				Tool: &engine.ToolEvent{ID: "t5", Name: "read_file"},
			},
			lastMilestone: "bg1",
			expected:      false,
		},
		{
			name: "非 ToolResult 不通知",
			event: engine.Event{
				Type: engine.EventToolCall,
				Tool: &engine.ToolEvent{ID: "bg1", Name: "背景工作"},
			},
			lastMilestone: "bg1",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldNotifyToolResult(tt.event, tt.lastMilestone)
			if got != tt.expected {
				t.Errorf("shouldNotifyToolResult() = %v, want %v", got, tt.expected)
			}
		})
	}
}
