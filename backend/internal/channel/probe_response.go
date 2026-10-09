package channel

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// A 200 only confirms the stream opened, not that the model ran successfully.
func validateTestResponse(response *http.Response, format Format) error {
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		body, err := io.ReadAll(io.LimitReader(response.Body, testBodyLimit+1))
		if err != nil {
			return fmt.Errorf("读取测试响应失败: %w", err)
		}
		if len(body) > testBodyLimit {
			return errors.New("测试响应超过大小限制")
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(body, &value) != nil || len(value) == 0 {
			return errors.New("测试响应不是有效 JSON")
		}
		_, err = testEvent(format, body)
		return err
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, testBodyLimit+1))
	scanner.Buffer(make([]byte, 4096), testBodyLimit+1)
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) == 0 {
				continue
			}
			done, err := testEvent(format, []byte(strings.Join(data, "\n")))
			data = nil
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取测试流失败: %w", err)
	}
	return errors.New("测试流未正常结束或超过大小限制")
}

func testEvent(format Format, data []byte) (bool, error) {
	if string(data) == "[DONE]" && format == FormatOpenAIChat {
		return true, nil
	}
	var event struct {
		Status   string          `json:"status"`
		Type     string          `json:"type"`
		Error    json.RawMessage `json:"error"`
		Response struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"response"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(data, &event) != nil {
		return false, errors.New("测试响应数据无效")
	}
	hasError := func(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }
	if hasError(event.Error) || hasError(event.Response.Error) || event.Type == "error" || event.Type == "response.failed" || event.Response.Status == "failed" || event.Status == "failed" {
		return false, errors.New("上游返回测试错误事件")
	}
	switch format {
	case FormatOpenAIResponses:
		return event.Type == "response.completed" || event.Type == "response.incomplete", nil
	case FormatAnthropic:
		return event.Type == "message_stop", nil
	case FormatGemini:
		for _, candidate := range event.Candidates {
			if candidate.FinishReason != "" {
				return true, nil
			}
		}
	}
	return false, nil
}
