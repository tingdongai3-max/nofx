package provider

import "testing"

func TestMiniMaxParseMCPResponseFullKeepsThinkingAndMultipleTextBlocks(t *testing.T) {
	client := NewMiniMaxClient().(*MiniMaxClient)

	body := []byte(`{
		"content": [
			{"type": "thinking", "thinking": "first thinking block"},
			{"type": "text", "text": "<reasoning>reasoning body</reasoning>"},
			{"type": "text", "text": "<decision>[{\"symbol\":\"ALL\",\"action\":\"wait\"}]</decision>"}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 20
		}
	}`)

	resp, err := client.ParseMCPResponseFull(body)
	if err != nil {
		t.Fatalf("ParseMCPResponseFull returned error: %v", err)
	}

	want := "first thinking block\n\n<reasoning>reasoning body</reasoning>\n\n<decision>[{\"symbol\":\"ALL\",\"action\":\"wait\"}]</decision>"
	if resp.Content != want {
		t.Fatalf("unexpected content:\nwant: %q\ngot:  %q", want, resp.Content)
	}
}

func TestMiniMaxParseMCPResponseReturnsConcatenatedContent(t *testing.T) {
	client := NewMiniMaxClient().(*MiniMaxClient)

	body := []byte(`{
		"content": [
			{"type": "thinking", "thinking": "think"},
			{"type": "text", "text": "answer-a"},
			{"type": "text", "text": "answer-b"}
		],
		"usage": {
			"input_tokens": 1,
			"output_tokens": 2
		}
	}`)

	content, err := client.ParseMCPResponse(body)
	if err != nil {
		t.Fatalf("ParseMCPResponse returned error: %v", err)
	}

	want := "think\n\nanswer-a\n\nanswer-b"
	if content != want {
		t.Fatalf("unexpected content:\nwant: %q\ngot:  %q", want, content)
	}
}
